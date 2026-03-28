package parameterstore

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/raywall/go-code-blocks/core"
)

// GetParameter retrieves a single parameter value by name.
// Decryption is applied when WithDecryption was set on the block
// or when the parameter is a plain String / StringList.
func (b *Block) GetParameter(ctx context.Context, name string) (string, error) {
	if err := b.checkInit(); err != nil {
		return "", err
	}

	out, err := b.client.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           aws.String(b.prefixed(name)),
		WithDecryption: aws.Bool(b.cfg.withDecrypt),
	})
	if err != nil {
		return "", fmt.Errorf("parameterstore %q get %q: %w", b.name, name, err)
	}
	return aws.ToString(out.Parameter.Value), nil
}

// GetParameterDecrypted retrieves a SecureString parameter with decryption
// forced on, regardless of the block-level WithDecryption setting.
func (b *Block) GetParameterDecrypted(ctx context.Context, name string) (string, error) {
	if err := b.checkInit(); err != nil {
		return "", err
	}

	out, err := b.client.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           aws.String(b.prefixed(name)),
		WithDecryption: aws.Bool(true),
	})
	if err != nil {
		return "", fmt.Errorf("parameterstore %q get-decrypted %q: %w", b.name, name, err)
	}
	return aws.ToString(out.Parameter.Value), nil
}

// GetParametersByPath retrieves all parameters under path and returns a
// name → value map. Path is appended to the block's configured prefix.
// Pagination is handled internally.
func (b *Block) GetParametersByPath(ctx context.Context, path string) (map[string]string, error) {
	if err := b.checkInit(); err != nil {
		return nil, err
	}

	fullPath := b.prefixed(path)
	result := make(map[string]string)
	var nextToken *string

	for {
		out, err := b.client.GetParametersByPath(ctx, &ssm.GetParametersByPathInput{
			Path:           aws.String(fullPath),
			WithDecryption: aws.Bool(b.cfg.withDecrypt),
			Recursive:      aws.Bool(true),
			NextToken:      nextToken,
		})
		if err != nil {
			return nil, fmt.Errorf("parameterstore %q get-by-path %q: %w", b.name, path, err)
		}

		for _, p := range out.Parameters {
			result[aws.ToString(p.Name)] = aws.ToString(p.Value)
		}

		if out.NextToken == nil {
			break
		}
		nextToken = out.NextToken
	}

	return result, nil
}

// PutParameter creates or updates a parameter.
// paramType should be types.ParameterTypeString, types.ParameterTypeStringList,
// or types.ParameterTypeSecureString.
func (b *Block) PutParameter(ctx context.Context, name, value string, paramType types.ParameterType, overwrite bool) error {
	if err := b.checkInit(); err != nil {
		return err
	}

	_, err := b.client.PutParameter(ctx, &ssm.PutParameterInput{
		Name:      aws.String(b.prefixed(name)),
		Value:     aws.String(value),
		Type:      paramType,
		Overwrite: aws.Bool(overwrite),
	})
	if err != nil {
		return fmt.Errorf("parameterstore %q put %q: %w", b.name, name, err)
	}
	return nil
}

// DeleteParameter removes a parameter by name.
func (b *Block) DeleteParameter(ctx context.Context, name string) error {
	if err := b.checkInit(); err != nil {
		return err
	}

	_, err := b.client.DeleteParameter(ctx, &ssm.DeleteParameterInput{
		Name: aws.String(b.prefixed(name)),
	})
	if err != nil {
		return fmt.Errorf("parameterstore %q delete %q: %w", b.name, name, err)
	}
	return nil
}

// ── helpers ──────────────────────────────────────────────────────────────────

func (b *Block) checkInit() error {
	if b.client == nil {
		return fmt.Errorf("parameterstore %q: %w", b.name, core.ErrNotInitialized)
	}
	return nil
}

func (b *Block) prefixed(name string) string {
	if b.cfg.pathPrefix == "" {
		return name
	}
	prefix := strings.TrimSuffix(b.cfg.pathPrefix, "/")
	if !strings.HasPrefix(name, "/") {
		return prefix + "/" + name
	}
	return prefix + name
}
