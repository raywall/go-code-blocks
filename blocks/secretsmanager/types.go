package secretsmanager

import (
	"time"

	awssm "github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/raywall/go-code-blocks/internal/awscfg"
)

// Block is an AWS Secrets Manager integration block.
type Block struct {
	name   string
	cfg    blockConfig
	aws    *awscfg.Resolver
	client *awssm.Client
}

// SecretMetadata holds summary information about a secret, as returned
// by ListSecrets.
type SecretMetadata struct {
	Name        string
	ARN         string
	Description string
	LastChanged time.Time
}

// DeleteOptions controls the behaviour of DeleteSecret.
type DeleteOptions struct {
	// ForceDelete skips the 7–30 day recovery window and immediately removes
	// the secret. Use with caution — this is irreversible.
	ForceDelete bool
	// RecoveryWindowDays sets a custom recovery window (7–30 days).
	// Ignored when ForceDelete is true.
	RecoveryWindowDays int32
}
