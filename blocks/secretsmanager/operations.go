package secretsmanager

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssm "github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/raywall/go-code-blocks/core"
)

// GetSecret retrieves the current string value of a secret by name or ARN.
// For binary secrets, use GetSecretBinary instead.
func (b *Block) GetSecret(ctx context.Context, name string) (string, error) {
	if err := b.checkInit(); err != nil {
		return "", err
	}

	out, err := b.client.GetSecretValue(ctx, &awssm.GetSecretValueInput{
		SecretId:     aws.String(name),
		VersionStage: aws.String(b.cfg.versionStage),
	})
	if err != nil {
		return "", fmt.Errorf("secretsmanager %q get %q: %w", b.name, name, err)
	}
	if out.SecretString == nil {
		return "", fmt.Errorf("secretsmanager %q get %q: secret has no string value (binary?)", b.name, name)
	}
	return *out.SecretString, nil
}

// GetSecretBinary retrieves the current binary value of a secret.
func (b *Block) GetSecretBinary(ctx context.Context, name string) ([]byte, error) {
	if err := b.checkInit(); err != nil {
		return nil, err
	}

	out, err := b.client.GetSecretValue(ctx, &awssm.GetSecretValueInput{
		SecretId:     aws.String(name),
		VersionStage: aws.String(b.cfg.versionStage),
	})
	if err != nil {
		return nil, fmt.Errorf("secretsmanager %q get-binary %q: %w", b.name, name, err)
	}
	return out.SecretBinary, nil
}

// GetSecretJSON retrieves a secret and unmarshals its JSON value into v.
// This is the idiomatic way to work with structured secrets (database
// credentials, API keys with multiple fields, etc.).
//
//	type DBCreds struct { Host, User, Password string }
//	var creds DBCreds
//	if err := secrets.GetSecretJSON(ctx, "prod/db", &creds); err != nil { … }
func (b *Block) GetSecretJSON(ctx context.Context, name string, v any) error {
	raw, err := b.GetSecret(ctx, name)
	if err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(raw), v); err != nil {
		return fmt.Errorf("secretsmanager %q get-json %q: unmarshal: %w", b.name, name, err)
	}
	return nil
}

// GetSecretVersion retrieves a specific version of a secret.
// Useful for accessing a previous secret value during rotation.
func (b *Block) GetSecretVersion(ctx context.Context, name, versionID string) (string, error) {
	if err := b.checkInit(); err != nil {
		return "", err
	}

	out, err := b.client.GetSecretValue(ctx, &awssm.GetSecretValueInput{
		SecretId:  aws.String(name),
		VersionId: aws.String(versionID),
	})
	if err != nil {
		return "", fmt.Errorf("secretsmanager %q get-version %q@%q: %w", b.name, name, versionID, err)
	}
	if out.SecretString == nil {
		return "", fmt.Errorf("secretsmanager %q get-version %q: secret has no string value", b.name, name)
	}
	return *out.SecretString, nil
}

// CreateSecret creates a new string secret.
// description is optional; pass an empty string to omit it.
func (b *Block) CreateSecret(ctx context.Context, name, value, description string) (string, error) {
	if err := b.checkInit(); err != nil {
		return "", err
	}

	input := &awssm.CreateSecretInput{
		Name:         aws.String(name),
		SecretString: aws.String(value),
	}
	if description != "" {
		input.Description = aws.String(description)
	}

	out, err := b.client.CreateSecret(ctx, input)
	if err != nil {
		return "", fmt.Errorf("secretsmanager %q create %q: %w", b.name, name, err)
	}
	return aws.ToString(out.ARN), nil
}

// CreateSecretJSON marshals v to JSON and creates a new structured secret.
// Returns the ARN of the created secret.
func (b *Block) CreateSecretJSON(ctx context.Context, name string, v any, description string) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("secretsmanager %q create-json %q: marshal: %w", b.name, name, err)
	}
	return b.CreateSecret(ctx, name, string(data), description)
}

// UpdateSecret replaces the value of an existing secret.
// Secrets Manager automatically creates a new version and stages it as
// AWSCURRENT while moving the previous value to AWSPREVIOUS.
func (b *Block) UpdateSecret(ctx context.Context, name, value string) error {
	if err := b.checkInit(); err != nil {
		return err
	}

	_, err := b.client.UpdateSecret(ctx, &awssm.UpdateSecretInput{
		SecretId:     aws.String(name),
		SecretString: aws.String(value),
	})
	if err != nil {
		return fmt.Errorf("secretsmanager %q update %q: %w", b.name, name, err)
	}
	return nil
}

// UpdateSecretJSON marshals v to JSON and updates an existing structured secret.
func (b *Block) UpdateSecretJSON(ctx context.Context, name string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("secretsmanager %q update-json %q: marshal: %w", b.name, name, err)
	}
	return b.UpdateSecret(ctx, name, string(data))
}

// DeleteSecret schedules a secret for deletion.
// By default a 30-day recovery window applies. Use DeleteOptions to customise
// the window or force an immediate, irreversible deletion.
func (b *Block) DeleteSecret(ctx context.Context, name string, opts DeleteOptions) error {
	if err := b.checkInit(); err != nil {
		return err
	}

	input := &awssm.DeleteSecretInput{
		SecretId: aws.String(name),
	}

	if opts.ForceDelete {
		input.ForceDeleteWithoutRecovery = aws.Bool(true)
	} else if opts.RecoveryWindowDays > 0 {
		input.RecoveryWindowInDays = aws.Int64(int64(opts.RecoveryWindowDays))
	}

	_, err := b.client.DeleteSecret(ctx, input)
	if err != nil {
		return fmt.Errorf("secretsmanager %q delete %q: %w", b.name, name, err)
	}
	return nil
}

// ListSecrets returns metadata for all secrets in the account/region.
// Pagination is handled internally.
func (b *Block) ListSecrets(ctx context.Context) ([]SecretMetadata, error) {
	if err := b.checkInit(); err != nil {
		return nil, err
	}

	var (
		results   []SecretMetadata
		nextToken *string
	)

	for {
		out, err := b.client.ListSecrets(ctx, &awssm.ListSecretsInput{
			NextToken: nextToken,
		})
		if err != nil {
			return nil, fmt.Errorf("secretsmanager %q list: %w", b.name, err)
		}

		for _, s := range out.SecretList {
			meta := SecretMetadata{
				Name:        aws.ToString(s.Name),
				ARN:         aws.ToString(s.ARN),
				Description: aws.ToString(s.Description),
			}
			if s.LastChangedDate != nil {
				meta.LastChanged = *s.LastChangedDate
			}
			results = append(results, meta)
		}

		if out.NextToken == nil {
			break
		}
		nextToken = out.NextToken
	}

	return results, nil
}

// RotateSecret immediately triggers the rotation Lambda associated with
// the secret. Rotation must have been previously configured.
func (b *Block) RotateSecret(ctx context.Context, name string) error {
	if err := b.checkInit(); err != nil {
		return err
	}

	_, err := b.client.RotateSecret(ctx, &awssm.RotateSecretInput{
		SecretId:          aws.String(name),
		RotateImmediately: aws.Bool(true),
	})
	if err != nil {
		return fmt.Errorf("secretsmanager %q rotate %q: %w", b.name, name, err)
	}
	return nil
}

// ── helpers ──────────────────────────────────────────────────────────────────

func (b *Block) checkInit() error {
	if b.client == nil {
		return fmt.Errorf("secretsmanager %q: %w", b.name, core.ErrNotInitialized)
	}
	return nil
}
