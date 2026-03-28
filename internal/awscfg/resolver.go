// Package awscfg provides a shared AWS configuration resolver used by all
// AWS-backed blocks. It decouples blocks from the specifics of credential
// loading while keeping the public API minimal.
package awscfg

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
)

// Resolver builds an aws.Config from a prioritised set of options.
//
// Priority (highest first):
//  1. Explicit aws.Config via WithConfig
//  2. Environment variables / default credential chain (with optional overrides)
type Resolver struct {
	cfg      *aws.Config // pre-built config; skips all other resolution when set
	region   string
	profile  string
	endpoint string // custom endpoint override (LocalStack, DynamoDB Local, etc.)
}

// Option configures a Resolver.
type Option func(*Resolver)

// WithConfig injects a fully-built aws.Config, bypassing all automatic
// credential and region resolution. Use this when you manage the AWS config
// yourself (e.g. in tests or multi-account setups).
func WithConfig(cfg aws.Config) Option {
	return func(r *Resolver) { r.cfg = &cfg }
}

// WithRegion sets the AWS region. Ignored when WithConfig is provided.
func WithRegion(region string) Option {
	return func(r *Resolver) { r.region = region }
}

// WithProfile selects a named profile from the AWS shared credentials file.
// Ignored when WithConfig is provided.
func WithProfile(profile string) Option {
	return func(r *Resolver) { r.profile = profile }
}

// WithEndpoint overrides the service endpoint URL.
// Used for local development with LocalStack, DynamoDB Local, MinIO, etc.
func WithEndpoint(endpoint string) Option {
	return func(r *Resolver) { r.endpoint = endpoint }
}

// New creates a Resolver from the supplied options.
func New(opts []Option) *Resolver {
	r := &Resolver{}
	for _, o := range opts {
		o(r)
	}
	return r
}

// Resolve returns a valid aws.Config, loading defaults from the environment
// when no explicit config was provided.
func (r *Resolver) Resolve(ctx context.Context) (aws.Config, error) {
	if r.cfg != nil {
		return *r.cfg, nil
	}

	var lopts []func(*config.LoadOptions) error

	if r.region != "" {
		lopts = append(lopts, config.WithRegion(r.region))
	}
	if r.profile != "" {
		lopts = append(lopts, config.WithSharedConfigProfile(r.profile))
	}

	cfg, err := config.LoadDefaultConfig(ctx, lopts...)
	if err != nil {
		return aws.Config{}, fmt.Errorf("awscfg: load default config: %w", err)
	}
	return cfg, nil
}

// Endpoint returns the custom endpoint override (empty string if not set).
func (r *Resolver) Endpoint() string { return r.endpoint }
