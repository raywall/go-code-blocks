package dynamodb

import (
	awsdynamodb "github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/raywall/go-code-blocks/internal/awscfg"
)

// Block is a typed DynamoDB integration block.
//
// T is the Go struct that maps to a DynamoDB item via the `dynamodbav` struct tag.
// All CRUD operations are strongly typed over T, eliminating manual marshalling.
type Block[T any] struct {
	name   string
	cfg    blockConfig
	aws    *awscfg.Resolver
	client *awsdynamodb.Client
}

// ── Query helpers ────────────────────────────────────────────────────────────

// QueryInput parametrises a DynamoDB Query operation.
type QueryInput struct {
	// KeyConditionExpression is required (e.g. "id = :pk AND begins_with(sk, :prefix)").
	KeyConditionExpression string
	// ExpressionAttributeNames maps placeholder tokens to actual attribute names.
	ExpressionAttributeNames map[string]string
	// ExpressionAttributeValues maps value placeholders to AttributeValues.
	ExpressionAttributeValues map[string]types.AttributeValue
	// FilterExpression is an optional server-side filter applied after the query.
	FilterExpression string
	// IndexName targets a Global or Local Secondary Index instead of the base table.
	IndexName string
	// Limit caps the number of items evaluated (not returned) per call.
	Limit *int32
	// ScanIndexForward controls sort order; nil defaults to ascending.
	ScanIndexForward *bool
	// LastEvaluatedKey is the pagination token returned by the previous call.
	LastEvaluatedKey map[string]types.AttributeValue
}

// Page is a paginated result set.
type Page[T any] struct {
	Items            []T
	LastEvaluatedKey map[string]types.AttributeValue
	Count            int32
	ScannedCount     int32
}
