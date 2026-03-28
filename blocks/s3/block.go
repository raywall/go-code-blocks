// Package s3 provides an S3 integration block with a clean object storage API.
package s3

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/raywall/go-code-blocks/internal/awscfg"
)

// New creates a new S3 Block.
//
//	block := s3.New("assets",
//	    s3.WithRegion("us-east-1"),
//	    s3.WithBucket("my-assets-bucket"),
//	    s3.WithKeyPrefix("uploads/"),
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
		return fmt.Errorf("s3 %q: resolve aws config: %w", b.name, err)
	}

	var clientOpts []func(*awss3.Options)

	if ep := b.aws.Endpoint(); ep != "" {
		clientOpts = append(clientOpts, func(o *awss3.Options) {
			o.BaseEndpoint = aws.String(ep)
		})
	}
	if b.cfg.usePathStyle {
		clientOpts = append(clientOpts, func(o *awss3.Options) {
			o.UsePathStyle = true
		})
	}

	b.client = awss3.NewFromConfig(awsCfg, clientOpts...)
	b.presign = awss3.NewPresignClient(b.client)
	return nil
}

// Shutdown implements core.Block.
func (b *Block) Shutdown(_ context.Context) error { return nil }
