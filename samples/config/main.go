// samples/config/main.go
//
// Demonstra o uso do bloco SSM Parameter Store de forma isolada.
//
// Operações cobertas:
//   - GetParameter           — leitura de parâmetro individual
//   - GetParameterDecrypted  — leitura forçando descriptografia
//   - GetParametersByPath    — leitura em lote por hierarquia (paginação automática)
//   - PutParameter           — criação / atualização de parâmetro
//   - DeleteParameter        — remoção de parâmetro
//
// Hierarquia usada neste exemplo:
//
//	/myapp/dev/
//	  ├── database/host
//	  ├── database/port
//	  ├── feature-flags/new-checkout
//	  └── secrets/api-key          (SecureString)
//
// Variáveis de ambiente:
//
//	AWS_REGION       região AWS (default: us-east-1)
//	SSM_PATH_PREFIX  prefixo base (default: /myapp/dev)
package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/raywall/go-code-blocks/blocks/parameterstore"
	"github.com/raywall/go-code-blocks/core"
)

func main() {
	ctx := context.Background()

	cfg := parameterstore.New("config",
		parameterstore.WithRegion(envOr("AWS_REGION", "us-east-1")),
		parameterstore.WithPathPrefix(envOr("SSM_PATH_PREFIX", "/myapp/dev")),
		parameterstore.WithDecryption(), // descriptografa SecureString automaticamente
	)

	app := core.NewContainer()
	app.MustRegister(cfg)

	if err := app.InitAll(ctx); err != nil {
		slog.Error("init failed", "err", err)
		os.Exit(1)
	}
	defer app.ShutdownAll(ctx)

	// ── 1. Escrever parâmetros de configuração ────────────────────────────────
	slog.Info("── PutParameter (String) ──")
	stringParams := map[string]string{
		"database/host":              "db.internal.example.com",
		"database/port":              "5432",
		"feature-flags/new-checkout": "true",
		"feature-flags/dark-mode":    "false",
		"app/log-level":              "info",
		"app/max-connections":        "50",
	}
	for name, value := range stringParams {
		if err := cfg.PutParameter(ctx, name, value, types.ParameterTypeString, true); err != nil {
			slog.Error("PutParameter", "name", name, "err", err)
			continue
		}
		slog.Info("parameter written", "name", name)
	}

	// ── 2. Escrever SecureString (dado sensível criptografado) ────────────────
	slog.Info("── PutParameter (SecureString) ──")
	if err := cfg.PutParameter(
		ctx,
		"secrets/api-key",
		"super-secret-api-key-12345",
		types.ParameterTypeSecureString,
		true,
	); err != nil {
		slog.Error("PutParameter SecureString", "err", err)
	} else {
		slog.Info("secure parameter written", "name", "secrets/api-key")
	}

	// ── 3. Ler parâmetro individual ───────────────────────────────────────────
	slog.Info("── GetParameter ──")
	host, err := cfg.GetParameter(ctx, "database/host")
	if err != nil {
		slog.Error("GetParameter host", "err", err)
	} else {
		slog.Info("database host", "value", host)
	}

	flag, err := cfg.GetParameter(ctx, "feature-flags/new-checkout")
	if err != nil {
		slog.Error("GetParameter flag", "err", err)
	} else {
		slog.Info("feature flag", "new-checkout", flag)
	}

	// ── 4. Ler SecureString com descriptografia explícita ─────────────────────
	// Útil quando o bloco foi criado sem WithDecryption mas um parâmetro
	// específico precisa ser descriptografado.
	slog.Info("── GetParameterDecrypted ──")
	apiKey, err := cfg.GetParameterDecrypted(ctx, "secrets/api-key")
	if err != nil {
		slog.Error("GetParameterDecrypted", "err", err)
	} else {
		// Nunca logar segredos em produção — apenas ilustrativo.
		slog.Info("api key fetched", "length", len(apiKey))
	}

	// ── 5. Leitura em lote por caminho hierárquico ────────────────────────────
	slog.Info("── GetParametersByPath (all) ──")
	all, err := cfg.GetParametersByPath(ctx, "/")
	if err != nil {
		slog.Error("GetParametersByPath all", "err", err)
	} else {
		slog.Info("all parameters", "count", len(all))
		for k, v := range all {
			// Mascarar SecureStrings no log
			display := v
			if len(display) > 4 {
				display = display[:4] + "****"
			}
			slog.Info("  param", "name", k, "value", display)
		}
	}

	// ── 6. Leitura de subárvore específica ───────────────────────────────────
	slog.Info("── GetParametersByPath (database/) ──")
	dbParams, err := cfg.GetParametersByPath(ctx, "database/")
	if err != nil {
		slog.Error("GetParametersByPath database/", "err", err)
	} else {
		slog.Info("database params", "count", len(dbParams))
		for k, v := range dbParams {
			slog.Info("  db param", "name", k, "value", v)
		}
	}

	slog.Info("── GetParametersByPath (feature-flags/) ──")
	flags, err := cfg.GetParametersByPath(ctx, "feature-flags/")
	if err != nil {
		slog.Error("GetParametersByPath feature-flags/", "err", err)
	} else {
		for k, v := range flags {
			slog.Info("  flag", "name", k, "enabled", v)
		}
	}

	// ── 7. Deletar parâmetros ─────────────────────────────────────────────────
	slog.Info("── DeleteParameter ──")
	toDelete := []string{
		"database/host",
		"database/port",
		"feature-flags/new-checkout",
		"feature-flags/dark-mode",
		"app/log-level",
		"app/max-connections",
		"secrets/api-key",
	}
	for _, name := range toDelete {
		if err := cfg.DeleteParameter(ctx, name); err != nil {
			slog.Error("DeleteParameter", "name", name, "err", err)
		} else {
			slog.Info("parameter deleted", "name", name)
		}
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
