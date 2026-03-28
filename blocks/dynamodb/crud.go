package dynamodb

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	awsdynamodb "github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/raywall/go-code-blocks/core"
)

// PutItem marshals item into a DynamoDB AttributeValue map and writes it to the
// configured table. It performs a full replace if the key already exists.
func (b *Block[T]) PutItem(ctx context.Context, item T) error {
	if err := b.checkInit(); err != nil {
		return err
	}

	av, err := attributevalue.MarshalMap(item)
	if err != nil {
		return fmt.Errorf("dynamodb %q put: marshal: %w", b.name, err)
	}

	_, err = b.client.PutItem(ctx, &awsdynamodb.PutItemInput{
		TableName: aws.String(b.cfg.tableName),
		Item:      av,
	})
	if err != nil {
		return fmt.Errorf("dynamodb %q put: %w", b.name, err)
	}
	return nil
}

// GetItem retrieves a single item by its primary key.
// sortKeyValue is ignored when the table has no sort key (WithSortKey was not set).
// Returns core.ErrItemNotFound when the item does not exist.
func (b *Block[T]) GetItem(ctx context.Context, partitionKeyValue, sortKeyValue any) (T, error) {
	var zero T
	if err := b.checkInit(); err != nil {
		return zero, err
	}

	key, err := b.buildKey(partitionKeyValue, sortKeyValue)
	if err != nil {
		return zero, fmt.Errorf("dynamodb %q get: build key: %w", b.name, err)
	}

	out, err := b.client.GetItem(ctx, &awsdynamodb.GetItemInput{
		TableName: aws.String(b.cfg.tableName),
		Key:       key,
	})
	if err != nil {
		return zero, fmt.Errorf("dynamodb %q get: %w", b.name, err)
	}
	if out.Item == nil {
		return zero, fmt.Errorf("dynamodb %q get: %w", b.name, core.ErrItemNotFound)
	}

	var item T
	if err := attributevalue.UnmarshalMap(out.Item, &item); err != nil {
		return zero, fmt.Errorf("dynamodb %q get: unmarshal: %w", b.name, err)
	}
	return item, nil
}

// DeleteItem removes a single item by its primary key.
// sortKeyValue is ignored when the table has no sort key.
func (b *Block[T]) DeleteItem(ctx context.Context, partitionKeyValue, sortKeyValue any) error {
	if err := b.checkInit(); err != nil {
		return err
	}

	key, err := b.buildKey(partitionKeyValue, sortKeyValue)
	if err != nil {
		return fmt.Errorf("dynamodb %q delete: build key: %w", b.name, err)
	}

	_, err = b.client.DeleteItem(ctx, &awsdynamodb.DeleteItemInput{
		TableName: aws.String(b.cfg.tableName),
		Key:       key,
	})
	if err != nil {
		return fmt.Errorf("dynamodb %q delete: %w", b.name, err)
	}
	return nil
}

// QueryItems executes a DynamoDB Query and returns a paginated Page[T].
// Use QueryInput.LastEvaluatedKey from a previous Page to fetch the next page.
func (b *Block[T]) QueryItems(ctx context.Context, in QueryInput) (Page[T], error) {
	if err := b.checkInit(); err != nil {
		return Page[T]{}, err
	}

	input := &awsdynamodb.QueryInput{
		TableName:                 aws.String(b.cfg.tableName),
		KeyConditionExpression:    aws.String(in.KeyConditionExpression),
		ExpressionAttributeValues: in.ExpressionAttributeValues,
		FilterExpression:          nilIfEmpty(in.FilterExpression),
		IndexName:                 nilIfEmpty(in.IndexName),
		Limit:                     in.Limit,
		ScanIndexForward:          in.ScanIndexForward,
		ExclusiveStartKey:         in.LastEvaluatedKey,
	}
	if len(in.ExpressionAttributeNames) > 0 {
		input.ExpressionAttributeNames = in.ExpressionAttributeNames
	}

	out, err := b.client.Query(ctx, input)
	if err != nil {
		return Page[T]{}, fmt.Errorf("dynamodb %q query: %w", b.name, err)
	}

	items := make([]T, 0, len(out.Items))
	for _, raw := range out.Items {
		var item T
		if err := attributevalue.UnmarshalMap(raw, &item); err != nil {
			return Page[T]{}, fmt.Errorf("dynamodb %q query: unmarshal: %w", b.name, err)
		}
		items = append(items, item)
	}

	return Page[T]{
		Items:            items,
		LastEvaluatedKey: out.LastEvaluatedKey,
		Count:            out.Count,
		ScannedCount:     out.ScannedCount,
	}, nil
}

// ScanItems performs a full table scan with optional pagination.
// Prefer QueryItems for production workloads; scans read every item.
func (b *Block[T]) ScanItems(ctx context.Context, limit *int32, lastKey map[string]types.AttributeValue) (Page[T], error) {
	if err := b.checkInit(); err != nil {
		return Page[T]{}, err
	}

	out, err := b.client.Scan(ctx, &awsdynamodb.ScanInput{
		TableName:         aws.String(b.cfg.tableName),
		Limit:             limit,
		ExclusiveStartKey: lastKey,
	})
	if err != nil {
		return Page[T]{}, fmt.Errorf("dynamodb %q scan: %w", b.name, err)
	}

	items := make([]T, 0, len(out.Items))
	for _, raw := range out.Items {
		var item T
		if err := attributevalue.UnmarshalMap(raw, &item); err != nil {
			return Page[T]{}, fmt.Errorf("dynamodb %q scan: unmarshal: %w", b.name, err)
		}
		items = append(items, item)
	}

	return Page[T]{
		Items:            items,
		LastEvaluatedKey: out.LastEvaluatedKey,
		Count:            out.Count,
		ScannedCount:     out.ScannedCount,
	}, nil
}

// ── helpers ──────────────────────────────────────────────────────────────────

func (b *Block[T]) checkInit() error {
	if b.client == nil {
		return fmt.Errorf("dynamodb %q: %w", b.name, core.ErrNotInitialized)
	}
	return nil
}

func (b *Block[T]) buildKey(pk, sk any) (map[string]types.AttributeValue, error) {
	if b.cfg.partitionKey == "" {
		return nil, errors.New("partition key name not configured; use WithPartitionKey")
	}

	pkAV, err := attributevalue.Marshal(pk)
	if err != nil {
		return nil, fmt.Errorf("marshal partition key: %w", err)
	}

	key := map[string]types.AttributeValue{b.cfg.partitionKey: pkAV}

	if b.cfg.sortKey != "" && sk != nil {
		skAV, err := attributevalue.Marshal(sk)
		if err != nil {
			return nil, fmt.Errorf("marshal sort key: %w", err)
		}
		key[b.cfg.sortKey] = skAV
	}
	return key, nil
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
