// Package parameterstore provides an AWS SSM Parameter Store integration block.
package parameterstore

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/raywall/go-code-blocks/internal/awscfg"
)

// Block is an SSM Parameter Store integration block.
type Block struct {
	name   string
	cfg    blockConfig
	aws    *awscfg.Resolver
	client *ssm.Client
}

// New creates a new Parameter Store Block.
//
//	block := parameterstore.New("config",
//	    parameterstore.WithRegion("us-east-1"),
//	    parameterstore.WithPathPrefix("/myapp/prod"),
//	    parameterstore.WithDecryption(),
//	)
func New(name string, opts ...Option) *Block {
	cfg := blockConfig{}
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
		return fmt.Errorf("parameterstore %q: resolve aws config: %w", b.name, err)
	}

	var clientOpts []func(*ssm.Options)
	if ep := b.aws.Endpoint(); ep != "" {
		clientOpts = append(clientOpts, func(o *ssm.Options) {
			o.BaseEndpoint = aws.String(ep)
		})
	}

	b.client = ssm.NewFromConfig(awsCfg, clientOpts...)
	return nil
}

// Shutdown implements core.Block.
func (b *Block) Shutdown(_ context.Context) error { return nil }
