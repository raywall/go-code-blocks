// samples/full-stack/main.go
//
// Simula um fluxo real de API: cadastro de usuário, cache, upload de avatar
// e carregamento de configuração. Combina todos os blocos disponíveis.
//
// Fluxo implementado:
//
//  1. Boot: carrega credenciais DB do Secrets Manager e feature flags do SSM
//  2. Request: salva usuário no DynamoDB
//  3. Cache: armazena perfil no Redis com TTL
//  4. Upload: sobe avatar para S3 e gera URL pré-assinada
//  5. Enriquecimento: lê permissões extras do DynamoDB via Query
//
// Variáveis de ambiente:
//
//	AWS_REGION        região AWS         (default: us-east-1)
//	USERS_TABLE       tabela de usuários (default: users-dev)
//	S3_BUCKET         bucket de assets   (default: my-assets-dev)
//	REDIS_ADDR        endereço Redis     (default: localhost:6379)
//	SSM_PATH_PREFIX   prefixo SSM        (default: /myapp/dev)
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/raywall/go-code-blocks/blocks/dynamodb"
	"github.com/raywall/go-code-blocks/blocks/parameterstore"
	"github.com/raywall/go-code-blocks/blocks/redis"
	"github.com/raywall/go-code-blocks/blocks/s3"
	"github.com/raywall/go-code-blocks/blocks/secretsmanager"
	"github.com/raywall/go-code-blocks/core"
)

// ── Modelos ───────────────────────────────────────────────────────────────────

type User struct {
	ID        string `dynamodbav:"id"         json:"id"`
	Email     string `dynamodbav:"email"      json:"email"`
	Name      string `dynamodbav:"name"       json:"name"`
	Plan      string `dynamodbav:"plan"       json:"plan"`
	AvatarURL string `dynamodbav:"avatar_url" json:"avatar_url"`
	CreatedAt string `dynamodbav:"created_at" json:"created_at"`
}

type AppConfig struct {
	LogLevel       string
	MaxConnections string
	NewCheckout    string
	DarkMode       string
}

type DBCredentials struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"`
	DBName   string `json:"dbname"`
}

// ── Nomes dos blocos ──────────────────────────────────────────────────────────

const (
	blockUsers   = "users"
	blockCache   = "cache"
	blockAssets  = "assets"
	blockConfig  = "config"
	blockSecrets = "secrets"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	app, err := buildContainer()
	if err != nil {
		slog.Error("container build failed", "err", err)
		os.Exit(1)
	}
	if err := app.InitAll(ctx); err != nil {
		slog.Error("init failed", "err", err)
		os.Exit(1)
	}
	defer app.ShutdownAll(context.Background())

	slog.Info("all blocks ready")

	// Recuperar os blocos tipados uma única vez
	users, _ := core.Get[*dynamodb.Block[User]](app, blockUsers)
	cache, _ := core.Get[*redis.Block](app, blockCache)
	assets, _ := core.Get[*s3.Block](app, blockAssets)
	cfg, _ := core.Get[*parameterstore.Block](app, blockConfig)
	secrets, _ := core.Get[*secretsmanager.Block](app, blockSecrets)

	// ── Fase 1: Boot — carregar configurações e segredos ──────────────────────
	slog.Info("═══ Fase 1: Boot ═══")
	appCfg := loadAppConfig(ctx, cfg)
	slog.Info("config loaded",
		"log_level", appCfg.LogLevel,
		"new_checkout", appCfg.NewCheckout,
	)

	var dbCreds DBCredentials
	if err := secrets.GetSecretJSON(ctx, "myapp/dev/database", &dbCreds); err != nil {
		slog.Warn("could not load DB credentials (continuing)", "err", err)
	} else {
		slog.Info("DB credentials loaded", "host", dbCreds.Host, "dbname", dbCreds.DBName)
	}

	// ── Fase 2: Request — processar cadastro de usuário ───────────────────────
	slog.Info("═══ Fase 2: Cadastro de usuário ═══")
	u := User{
		ID:        "usr_" + fmt.Sprintf("%d", time.Now().UnixMilli()),
		Email:     "carol@example.com",
		Name:      "Carol",
		Plan:      "pro",
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}

	if err := users.PutItem(ctx, u); err != nil {
		slog.Error("PutItem", "err", err)
	} else {
		slog.Info("user saved to DynamoDB", "id", u.ID, "email", u.Email)
	}

	// ── Fase 3: Cache — armazenar perfil no Redis ─────────────────────────────
	slog.Info("═══ Fase 3: Cache ═══")
	cacheTTL := 30 * time.Minute
	if err := cache.SetJSON(ctx, "profile:"+u.ID, u, cacheTTL); err != nil {
		slog.Warn("cache write failed (non-fatal)", "err", err)
	} else {
		slog.Info("profile cached", "id", u.ID, "ttl", cacheTTL)
	}

	// Verificar hit de cache antes de ir ao banco
	var cached User
	if err := cache.GetJSON(ctx, "profile:"+u.ID, &cached); err == nil {
		slog.Info("cache HIT", "id", cached.ID, "name", cached.Name)
	} else {
		slog.Info("cache MISS — would fall through to DynamoDB")
	}

	// Sessão via hash
	sessionKey := "session:" + u.ID
	if err := cache.HSet(ctx, sessionKey,
		"uid", u.ID,
		"plan", u.Plan,
		"logged_at", time.Now().Unix(),
	); err == nil {
		cache.Expire(ctx, sessionKey, 24*time.Hour)
		slog.Info("session created", "key", sessionKey)
	}

	// ── Fase 4: Upload de avatar para S3 ─────────────────────────────────────
	slog.Info("═══ Fase 4: Upload de avatar ═══")
	// Simula bytes de imagem
	fakePNG := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52}
	avatarKey := "avatars/" + u.ID + ".png"

	if err := assets.PutObject(
		ctx,
		avatarKey,
		bytes.NewReader(fakePNG),
		s3.WithContentType("image/png"),
		s3.WithMetadata(map[string]string{
			"owner": u.ID,
			"plan":  u.Plan,
		}),
	); err != nil {
		slog.Warn("avatar upload failed (non-fatal)", "err", err)
	} else {
		slog.Info("avatar uploaded", "key", avatarKey)

		// Gerar URL pré-assinada e atualizar perfil
		presignURL, err := assets.PresignGetURL(ctx, avatarKey, 1*time.Hour)
		if err != nil {
			slog.Warn("presign failed", "err", err)
		} else {
			slog.Info("avatar presigned URL generated", "expires_in", "1h")

			// Atualizar o usuário no DynamoDB com a URL do avatar
			u.AvatarURL = presignURL
			if err := users.PutItem(ctx, u); err != nil {
				slog.Warn("update avatar_url in DynamoDB failed", "err", err)
			} else {
				slog.Info("user updated with avatar_url")
			}

			// Invalida o cache para que a próxima leitura traga o dado atualizado
			if err := cache.Delete(ctx, "profile:"+u.ID); err == nil {
				slog.Info("cache invalidated after avatar update")
			}
		}
	}

	// ── Fase 5: Leitura final — DynamoDB como fonte de verdade ───────────────
	slog.Info("═══ Fase 5: Leitura final ═══")
	// Tenta cache primeiro
	err = cache.GetJSON(ctx, "profile:"+u.ID, &cached)
	if errors.Is(err, core.ErrItemNotFound) || err != nil {
		slog.Info("cache MISS — reading from DynamoDB")
		fetched, err := users.GetItem(ctx, u.ID, nil)
		if err != nil {
			slog.Error("GetItem", "err", err)
		} else {
			slog.Info("user from DynamoDB",
				"id", fetched.ID,
				"name", fetched.Name,
				"plan", fetched.Plan,
				"avatar_url_set", fetched.AvatarURL != "",
			)
			// Popular cache novamente
			cache.SetJSON(ctx, "profile:"+fetched.ID, fetched, 30*time.Minute)
			slog.Info("cache repopulated")
		}
	} else {
		slog.Info("cache HIT on final read", "id", cached.ID)
	}

	// ── Limpeza do exemplo ────────────────────────────────────────────────────
	slog.Info("═══ Limpeza ═══")
	users.DeleteItem(ctx, u.ID, nil)
	cache.Delete(ctx, "profile:"+u.ID, sessionKey)
	assets.DeleteObject(ctx, avatarKey)
	slog.Info("cleanup complete")
}

// buildContainer monta e registra todos os blocos.
func buildContainer() (*core.Container, error) {
	region := envOr("AWS_REGION", "us-east-1")

	usersBlock := dynamodb.New[User](blockUsers,
		dynamodb.WithRegion(region),
		dynamodb.WithTable(envOr("USERS_TABLE", "users-dev")),
		dynamodb.WithPartitionKey("id"),
	)

	cacheBlock := redis.New(blockCache,
		redis.WithAddr(envOr("REDIS_ADDR", "localhost:6379")),
		redis.WithKeyPrefix("myapp:"),
		redis.WithDialTimeout(3*time.Second),
	)

	assetsBlock := s3.New(blockAssets,
		s3.WithRegion(region),
		s3.WithBucket(envOr("S3_BUCKET", "my-assets-dev")),
		s3.WithKeyPrefix("users/"),
	)

	cfgBlock := parameterstore.New(blockConfig,
		parameterstore.WithRegion(region),
		parameterstore.WithPathPrefix(envOr("SSM_PATH_PREFIX", "/myapp/dev")),
		parameterstore.WithDecryption(),
	)

	secretsBlock := secretsmanager.New(blockSecrets,
		secretsmanager.WithRegion(region),
	)

	app := core.NewContainer()
	for _, b := range []core.Block{usersBlock, cacheBlock, assetsBlock, cfgBlock, secretsBlock} {
		if err := app.Register(b); err != nil {
			return nil, err
		}
	}
	return app, nil
}

// loadAppConfig carrega configurações do Parameter Store com fallbacks seguros.
func loadAppConfig(ctx context.Context, cfg *parameterstore.Block) AppConfig {
	get := func(name, fallback string) string {
		v, err := cfg.GetParameter(ctx, name)
		if err != nil {
			slog.Debug("SSM param not found, using fallback", "name", name, "fallback", fallback)
			return fallback
		}
		return v
	}

	return AppConfig{
		LogLevel:       get("app/log-level", "info"),
		MaxConnections: get("app/max-connections", "10"),
		NewCheckout:    get("feature-flags/new-checkout", "false"),
		DarkMode:       get("feature-flags/dark-mode", "false"),
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
