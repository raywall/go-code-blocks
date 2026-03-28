package dynamodb

import (
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/raywall/go-code-blocks/internal/awscfg"
)

// Option configures a DynamoDB Block.
type Option func(*blockConfig)

// blockConfig aggregates all Block configuration in a single, non-generic struct
// so options do not need to carry a type parameter.
type blockConfig struct {
	tableName    string
	partitionKey string
	sortKey      string
	awsOpts      []awscfg.Option
}

// ── AWS configuration ────────────────────────────────────────────────────────

// WithAWSConfig injects a pre-built aws.Config, bypassing automatic resolution.
func WithAWSConfig(cfg aws.Config) Option {
	return func(c *blockConfig) {
		c.awsOpts = append(c.awsOpts, awscfg.WithConfig(cfg))
	}
}

// WithRegion sets the AWS region used to resolve credentials and endpoints.
func WithRegion(region string) Option {
	return func(c *blockConfig) {
		c.awsOpts = append(c.awsOpts, awscfg.WithRegion(region))
	}
}

// WithProfile selects a named AWS credentials profile.
func WithProfile(profile string) Option {
	return func(c *blockConfig) {
		c.awsOpts = append(c.awsOpts, awscfg.WithProfile(profile))
	}
}

// WithEndpoint overrides the DynamoDB endpoint (e.g. "http://localhost:8000"
// for DynamoDB Local or LocalStack).
func WithEndpoint(endpoint string) Option {
	return func(c *blockConfig) {
		c.awsOpts = append(c.awsOpts, awscfg.WithEndpoint(endpoint))
	}
}

// ── Table configuration ──────────────────────────────────────────────────────

// WithTable sets the DynamoDB table name.
func WithTable(name string) Option {
	return func(c *blockConfig) { c.tableName = name }
}

// WithPartitionKey sets the attribute name of the table's partition key.
func WithPartitionKey(pk string) Option {
	return func(c *blockConfig) { c.partitionKey = pk }
}

// WithSortKey sets the attribute name of the table's sort key.
// Omit this option for single-key tables.
func WithSortKey(sk string) Option {
	return func(c *blockConfig) { c.sortKey = sk }
}
