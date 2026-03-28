// samples/secrets/main.go
//
// Demonstra o uso do bloco Secrets Manager de forma isolada.
//
// Operações cobertas:
//   - CreateSecret / CreateSecretJSON — criação de segredos string e JSON
//   - GetSecret / GetSecretJSON       — leitura de segredos
//   - GetSecretBinary                 — leitura de segredo binário
//   - GetSecretVersion                — leitura de versão específica
//   - UpdateSecret / UpdateSecretJSON — atualização de segredo
//   - ListSecrets                     — listagem de todos os segredos
//   - RotateSecret                    — rotação imediata (requer Lambda configurada)
//   - DeleteSecret                    — remoção com e sem recovery window
//
// Variáveis de ambiente:
//
//	AWS_REGION  região AWS (default: us-east-1)
package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/raywall/go-code-blocks/blocks/secretsmanager"
	"github.com/raywall/go-code-blocks/core"
)

// ── Modelos de segredos estruturados ─────────────────────────────────────────

type DatabaseCredentials struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"`
	DBName   string `json:"dbname"`
	SSLMode  string `json:"ssl_mode"`
}

type APIKey struct {
	Provider  string `json:"provider"`
	Key       string `json:"key"`
	SecretKey string `json:"secret_key,omitempty"`
	Endpoint  string `json:"endpoint,omitempty"`
}

func main() {
	ctx := context.Background()

	sm := secretsmanager.New("secrets",
		secretsmanager.WithRegion(envOr("AWS_REGION", "us-east-1")),
	)

	app := core.NewContainer()
	app.MustRegister(sm)

	if err := app.InitAll(ctx); err != nil {
		slog.Error("init failed", "err", err)
		os.Exit(1)
	}
	defer app.ShutdownAll(ctx)

	// ── 1. Criar segredo de string simples ────────────────────────────────────
	slog.Info("── CreateSecret (string) ──")
	arn, err := sm.CreateSecret(
		ctx,
		"myapp/dev/jwt-secret",
		"ultra-secret-jwt-signing-key-dont-share",
		"JWT signing key para myapp em dev",
	)
	if err != nil {
		slog.Error("CreateSecret string", "err", err)
	} else {
		slog.Info("secret created", "arn", arn)
	}

	// ── 2. Criar segredo estruturado (JSON) ───────────────────────────────────
	slog.Info("── CreateSecretJSON (database credentials) ──")
	dbCreds := DatabaseCredentials{
		Host:     "db.internal.example.com",
		Port:     5432,
		User:     "app_user",
		Password: "s3cr3t-p@ssw0rd",
		DBName:   "myapp_prod",
		SSLMode:  "require",
	}
	dbArn, err := sm.CreateSecretJSON(
		ctx,
		"myapp/dev/database",
		dbCreds,
		"Credenciais do banco de dados PostgreSQL",
	)
	if err != nil {
		slog.Error("CreateSecretJSON database", "err", err)
	} else {
		slog.Info("db credentials secret created", "arn", dbArn)
	}

	slog.Info("── CreateSecretJSON (API key) ──")
	apiKey := APIKey{
		Provider:  "stripe",
		Key:       "pk_test_abc123",
		SecretKey: "sk_test_xyz789",
		Endpoint:  "https://api.stripe.com",
	}
	_, err = sm.CreateSecretJSON(ctx, "myapp/dev/stripe", apiKey, "Stripe API keys")
	if err != nil {
		slog.Error("CreateSecretJSON stripe", "err", err)
	} else {
		slog.Info("stripe secret created")
	}

	// ── 3. Ler segredo de string ──────────────────────────────────────────────
	slog.Info("── GetSecret ──")
	jwtKey, err := sm.GetSecret(ctx, "myapp/dev/jwt-secret")
	if err != nil {
		slog.Error("GetSecret", "err", err)
	} else {
		// Nunca logar segredos reais — apenas ilustrativo.
		slog.Info("jwt secret fetched", "length", len(jwtKey), "preview", jwtKey[:8]+"...")
	}

	// ── 4. Ler segredo estruturado (JSON) ─────────────────────────────────────
	slog.Info("── GetSecretJSON ──")
	var fetchedCreds DatabaseCredentials
	if err := sm.GetSecretJSON(ctx, "myapp/dev/database", &fetchedCreds); err != nil {
		slog.Error("GetSecretJSON", "err", err)
	} else {
		slog.Info("db credentials loaded",
			"host", fetchedCreds.Host,
			"port", fetchedCreds.Port,
			"dbname", fetchedCreds.DBName,
			"ssl_mode", fetchedCreds.SSLMode,
		)
	}

	var fetchedKey APIKey
	if err := sm.GetSecretJSON(ctx, "myapp/dev/stripe", &fetchedKey); err != nil {
		slog.Error("GetSecretJSON stripe", "err", err)
	} else {
		slog.Info("stripe key loaded", "provider", fetchedKey.Provider, "endpoint", fetchedKey.Endpoint)
	}

	// ── 5. Atualizar segredo (gera nova versão AWSCURRENT) ────────────────────
	slog.Info("── UpdateSecret ──")
	if err := sm.UpdateSecret(ctx, "myapp/dev/jwt-secret", "nova-chave-jwt-rotacionada-2024"); err != nil {
		slog.Error("UpdateSecret", "err", err)
	} else {
		slog.Info("jwt secret updated — new version is AWSCURRENT")
	}

	slog.Info("── UpdateSecretJSON ──")
	updatedCreds := dbCreds
	updatedCreds.Password = "n0va-s3nh@-2024"
	if err := sm.UpdateSecretJSON(ctx, "myapp/dev/database", updatedCreds); err != nil {
		slog.Error("UpdateSecretJSON", "err", err)
	} else {
		slog.Info("db credentials updated")
	}

	// ── 6. Listar todos os segredos ───────────────────────────────────────────
	slog.Info("── ListSecrets ──")
	secrets, err := sm.ListSecrets(ctx)
	if err != nil {
		slog.Error("ListSecrets", "err", err)
	} else {
		slog.Info("secrets found", "count", len(secrets))
		for _, s := range secrets {
			slog.Info("  secret",
				"name", s.Name,
				"description", s.Description,
				"last_changed", s.LastChanged.Format("2006-01-02 15:04"),
			)
		}
	}

	// ── 7. Rotação (requer Lambda de rotação configurada no console) ──────────
	// slog.Info("── RotateSecret ──")
	// if err := sm.RotateSecret(ctx, "myapp/dev/database"); err != nil {
	// 	slog.Warn("RotateSecret (requires rotation Lambda)", "err", err)
	// } else {
	// 	slog.Info("rotation triggered")
	// }

	// ── 8. Deletar segredos ───────────────────────────────────────────────────
	slog.Info("── DeleteSecret (with recovery window) ──")
	// Soft delete — permanece recuperável por 7 dias
	if err := sm.DeleteSecret(ctx, "myapp/dev/stripe", secretsmanager.DeleteOptions{
		RecoveryWindowDays: 7,
	}); err != nil {
		slog.Error("DeleteSecret stripe", "err", err)
	} else {
		slog.Info("stripe secret scheduled for deletion", "recovery_window_days", 7)
	}

	slog.Info("── DeleteSecret (force, no recovery) ──")
	// Hard delete — imediato e irreversível. Use apenas em ambientes de dev/test.
	for _, name := range []string{"myapp/dev/jwt-secret", "myapp/dev/database"} {
		if err := sm.DeleteSecret(ctx, name, secretsmanager.DeleteOptions{ForceDelete: true}); err != nil {
			slog.Error("DeleteSecret force", "name", name, "err", err)
		} else {
			slog.Info("secret force-deleted", "name", name)
		}
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
