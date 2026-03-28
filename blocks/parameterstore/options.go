package parameterstore

import (
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/raywall/go-code-blocks/internal/awscfg"
)

// Option configures a Parameter Store Block.
type Option func(*blockConfig)

type blockConfig struct {
	pathPrefix  string // optional prefix applied to all parameter names
	withDecrypt bool   // default decryption behaviour for SecureString parameters
	awsOpts     []awscfg.Option
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

// WithEndpoint overrides the SSM endpoint.
func WithEndpoint(endpoint string) Option {
	return func(c *blockConfig) {
		c.awsOpts = append(c.awsOpts, awscfg.WithEndpoint(endpoint))
	}
}

// ── Parameter Store configuration ────────────────────────────────────────────

// WithPathPrefix sets a base path prepended to all parameter names.
// For example, "/myapp/prod" + "/db/password" → "/myapp/prod/db/password".
func WithPathPrefix(prefix string) Option {
	return func(c *blockConfig) { c.pathPrefix = prefix }
}

// WithDecryption enables automatic decryption of SecureString parameters
// for all Get operations on this block.
func WithDecryption() Option {
	return func(c *blockConfig) { c.withDecrypt = true }
}
