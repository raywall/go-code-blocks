// Package secretsmanager provides an AWS Secrets Manager integration block.
// It exposes a clean API for retrieving, creating, rotating, and deleting
// secrets, with first-class support for JSON-structured secret values.
package secretsmanager

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssm "github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/raywall/go-code-blocks/internal/awscfg"
)

const defaultVersionStage = "AWSCURRENT"

// New creates a new Secrets Manager Block.
//
//	block := secretsmanager.New("secrets",
//	    secretsmanager.WithRegion("us-east-1"),
//	)
func New(name string, opts ...Option) *Block {
	cfg := blockConfig{
		versionStage: defaultVersionStage,
	}
	for _, o := range opts {
		o(&cfg)
	}
	return &Block{
		name: name,
		cfg:  cfg,
		aws:  awscfg.New(cfg.awsOpts),
	}
}

// Name implements core.Block.
func (b *Block) Name() string { return b.name }

// Init implements core.Block.
func (b *Block) Init(ctx context.Context) error {
	awsCfg, err := b.aws.Resolve(ctx)
	if err != nil {
		return fmt.Errorf("secretsmanager %q: resolve aws config: %w", b.name, err)
	}

	var clientOpts []func(*awssm.Options)
	if ep := b.aws.Endpoint(); ep != "" {
		clientOpts = append(clientOpts, func(o *awssm.Options) {
			o.BaseEndpoint = aws.String(ep)
		})
	}

	b.client = awssm.NewFromConfig(awsCfg, clientOpts...)
	return nil
}

// Shutdown implements core.Block.
func (b *Block) Shutdown(_ context.Context) error { return nil }
