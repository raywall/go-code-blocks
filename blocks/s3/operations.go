package s3

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/raywall/go-code-blocks/core"
)

// PutObject uploads body under the given key.
// The key is automatically prefixed with the block's configured key prefix.
func (b *Block) PutObject(ctx context.Context, key string, body io.Reader, opts ...PutOption) error {
	if err := b.checkInit(); err != nil {
		return err
	}

	po := &putOptions{}
	for _, o := range opts {
		o(po)
	}

	input := &awss3.PutObjectInput{
		Bucket: aws.String(b.cfg.bucket),
		Key:    aws.String(b.prefixed(key)),
		Body:   body,
	}
	if po.contentType != "" {
		input.ContentType = aws.String(po.contentType)
	}
	if len(po.metadata) > 0 {
		input.Metadata = po.metadata
	}

	_, err := b.client.PutObject(ctx, input)
	if err != nil {
		return fmt.Errorf("s3 %q put %q: %w", b.name, key, err)
	}
	return nil
}

// GetObject retrieves an object by key.
// The caller is responsible for closing the returned ReadCloser.
func (b *Block) GetObject(ctx context.Context, key string) (io.ReadCloser, ObjectMetadata, error) {
	if err := b.checkInit(); err != nil {
		return nil, ObjectMetadata{}, err
	}

	out, err := b.client.GetObject(ctx, &awss3.GetObjectInput{
		Bucket: aws.String(b.cfg.bucket),
		Key:    aws.String(b.prefixed(key)),
	})
	if err != nil {
		return nil, ObjectMetadata{}, fmt.Errorf("s3 %q get %q: %w", b.name, key, err)
	}

	meta := ObjectMetadata{
		ContentLength: aws.ToInt64(out.ContentLength),
		ETag:          aws.ToString(out.ETag),
	}
	if out.ContentType != nil {
		meta.ContentType = *out.ContentType
	}
	if out.LastModified != nil {
		meta.LastModified = *out.LastModified
	}

	return out.Body, meta, nil
}

// DeleteObject removes a single object by key.
func (b *Block) DeleteObject(ctx context.Context, key string) error {
	if err := b.checkInit(); err != nil {
		return err
	}

	_, err := b.client.DeleteObject(ctx, &awss3.DeleteObjectInput{
		Bucket: aws.String(b.cfg.bucket),
		Key:    aws.String(b.prefixed(key)),
	})
	if err != nil {
		return fmt.Errorf("s3 %q delete %q: %w", b.name, key, err)
	}
	return nil
}

// ListObjects returns all objects whose key starts with prefix (relative to the
// block's configured key prefix). Pagination is handled internally.
func (b *Block) ListObjects(ctx context.Context, prefix string) ([]ObjectInfo, error) {
	if err := b.checkInit(); err != nil {
		return nil, err
	}

	var (
		results    []ObjectInfo
		pageToken  *string
		fullPrefix = b.prefixed(prefix)
	)

	for {
		out, err := b.client.ListObjectsV2(ctx, &awss3.ListObjectsV2Input{
			Bucket:            aws.String(b.cfg.bucket),
			Prefix:            aws.String(fullPrefix),
			ContinuationToken: pageToken,
		})
		if err != nil {
			return nil, fmt.Errorf("s3 %q list %q: %w", b.name, prefix, err)
		}

		for _, obj := range out.Contents {
			results = append(results, ObjectInfo{
				Key:          aws.ToString(obj.Key),
				Size:         aws.ToInt64(obj.Size),
				ETag:         aws.ToString(obj.ETag),
				LastModified: aws.ToTime(obj.LastModified),
			})
		}

		// IsTruncated is *bool in SDK v2 — dereference safely via aws.ToBool.
		if !aws.ToBool(out.IsTruncated) {
			break
		}
		pageToken = out.NextContinuationToken
	}

	return results, nil
}

// PresignGetURL generates a pre-signed GET URL that grants temporary access
// to an object without requiring AWS credentials.
func (b *Block) PresignGetURL(ctx context.Context, key string, expiry time.Duration) (string, error) {
	if err := b.checkInit(); err != nil {
		return "", err
	}

	req, err := b.presign.PresignGetObject(ctx, &awss3.GetObjectInput{
		Bucket: aws.String(b.cfg.bucket),
		Key:    aws.String(b.prefixed(key)),
	}, awss3.WithPresignExpires(expiry))
	if err != nil {
		return "", fmt.Errorf("s3 %q presign %q: %w", b.name, key, err)
	}
	return req.URL, nil
}

// ── helpers ──────────────────────────────────────────────────────────────────

func (b *Block) checkInit() error {
	if b.client == nil {
		return fmt.Errorf("s3 %q: %w", b.name, core.ErrNotInitialized)
	}
	return nil
}

func (b *Block) prefixed(key string) string {
	if b.cfg.keyPrefix == "" {
		return key
	}
	return strings.TrimSuffix(b.cfg.keyPrefix, "/") + "/" + strings.TrimPrefix(key, "/")
}
