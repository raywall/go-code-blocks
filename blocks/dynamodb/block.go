// Package dynamodb provides a typed DynamoDB integration block.
// It wraps the AWS SDK v2 DynamoDB client and exposes a minimal, strongly-typed
// CRUD API. The block is generic over T, the item model struct.
package dynamodb

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsdynamodb "github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/raywall/go-code-blocks/internal/awscfg"
)

// New creates a new DynamoDB Block with the given name and options.
//
//	block := dynamodb.New[User]("users",
//	    dynamodb.WithRegion("us-east-1"),
//	    dynamodb.WithTable("users-prod"),
//	    dynamodb.WithPartitionKey("id"),
//	)
func New[T any](name string, opts ...Option) *Block[T] {
	cfg := blockConfig{}
	for _, o := range opts {
		o(&cfg)
	}
	return &Block[T]{
		name: name,
		cfg:  cfg,
		aws:  awscfg.New(cfg.awsOpts),
	}
}

// Name implements core.Block.
func (b *Block[T]) Name() string { return b.name }

// Init implements core.Block. It resolves the AWS config and creates the
// DynamoDB client. A custom endpoint is applied here if WithEndpoint was set.
func (b *Block[T]) Init(ctx context.Context) error {
	awsCfg, err := b.aws.Resolve(ctx)
	if err != nil {
		return fmt.Errorf("dynamodb %q: resolve aws config: %w", b.name, err)
	}

	var clientOpts []func(*awsdynamodb.Options)
	if ep := b.aws.Endpoint(); ep != "" {
		clientOpts = append(clientOpts, func(o *awsdynamodb.Options) {
			o.BaseEndpoint = aws.String(ep)
		})
	}

	b.client = awsdynamodb.NewFromConfig(awsCfg, clientOpts...)
	return nil
}

// Shutdown implements core.Block. The DynamoDB SDK v2 client requires no
// explicit teardown, but the method is present for interface compliance.
func (b *Block[T]) Shutdown(_ context.Context) error { return nil }
