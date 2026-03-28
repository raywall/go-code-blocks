package s3

import (
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/raywall/go-code-blocks/internal/awscfg"
)

// Option configures an S3 Block.
type Option func(*blockConfig)

type blockConfig struct {
	bucket       string
	keyPrefix    string // optional key prefix applied to all operations
	usePathStyle bool   // required for LocalStack / MinIO
	awsOpts      []awscfg.Option
}

// ── AWS configuration ────────────────────────────────────────────────────────

// WithAWSConfig injects a pre-built aws.Config.
func WithAWSConfig(cfg aws.Config) Option {
	return func(c *blockConfig) {
		c.awsOpts = append(c.awsOpts, awscfg.WithConfig(cfg))
	}
}

// WithRegion sets the AWS region.
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

// WithEndpoint overrides the S3 endpoint (e.g. "http://localhost:4566" for LocalStack).
// Automatically enables path-style addressing.
func WithEndpoint(endpoint string) Option {
	return func(c *blockConfig) {
		c.awsOpts = append(c.awsOpts, awscfg.WithEndpoint(endpoint))
		c.usePathStyle = true
	}
}

// ── Bucket configuration ─────────────────────────────────────────────────────

// WithBucket sets the S3 bucket name.
func WithBucket(bucket string) Option {
	return func(c *blockConfig) { c.bucket = bucket }
}

// WithKeyPrefix sets an optional prefix prepended to every object key.
// Useful for namespacing objects within a shared bucket.
func WithKeyPrefix(prefix string) Option {
	return func(c *blockConfig) { c.keyPrefix = prefix }
}

// WithPathStyle enables S3 path-style addressing.
// Required for MinIO and similar S3-compatible services.
func WithPathStyle() Option {
	return func(c *blockConfig) { c.usePathStyle = true }
}
