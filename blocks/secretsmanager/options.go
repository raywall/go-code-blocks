package secretsmanager

import (
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/raywall/go-code-blocks/internal/awscfg"
)

// Option configures a Secrets Manager Block.
type Option func(*blockConfig)

type blockConfig struct {
	versionStage string // default: "AWSCURRENT"
	awsOpts      []awscfg.Option
}

// ── AWS configuration ────────────────────────────────────────────────────────

// WithAWSConfig injects a pre-built aws.Config, bypassing automatic resolution.
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

// WithEndpoint overrides the Secrets Manager endpoint (e.g. LocalStack).
func WithEndpoint(endpoint string) Option {
	return func(c *blockConfig) {
		c.awsOpts = append(c.awsOpts, awscfg.WithEndpoint(endpoint))
	}
}

// ── Secrets Manager configuration ────────────────────────────────────────────

// WithVersionStage sets the default version stage for GetSecret operations.
// Defaults to "AWSCURRENT" when not provided.
func WithVersionStage(stage string) Option {
	return func(c *blockConfig) { c.versionStage = stage }
}
