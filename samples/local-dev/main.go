// samples/local-dev/main.go
//
// Demonstra como configurar todos os blocos para desenvolvimento local,
// apontando para serviços locais em vez da AWS real.
//
// Serviços usados:
//   - DynamoDB Local  → http://localhost:8000  (amazon/dynamodb-local)
//   - LocalStack      → http://localhost:4566  (S3 + SSM)
//   - Redis           → localhost:6379
//
// Para subir o ambiente:
//
//	docker compose up -d
//
// O docker-compose.yml está no mesmo diretório deste arquivo.
//
// Variáveis opcionais (todos têm defaults locais):
//
//	DYNAMO_ENDPOINT   (default: http://localhost:8000)
//	LOCALSTACK_URL    (default: http://localhost:4566)
//	REDIS_ADDR        (default: localhost:6379)
package main

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/raywall/go-code-blocks/blocks/dynamodb"
	"github.com/raywall/go-code-blocks/blocks/parameterstore"
	"github.com/raywall/go-code-blocks/blocks/redis"
	"github.com/raywall/go-code-blocks/blocks/s3"
	"github.com/raywall/go-code-blocks/core"
)

type Product struct {
	ID       string  `dynamodbav:"id"       json:"id"`
	Name     string  `dynamodbav:"name"     json:"name"`
	Price    float64 `dynamodbav:"price"    json:"price"`
	Category string  `dynamodbav:"category" json:"category"`
}

func main() {
	ctx := context.Background()

	// aws.Config com credenciais fictícias — LocalStack e DynamoDB Local não
	// validam credenciais, mas o SDK exige que estejam presentes.
	fakeCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("test", "test", "test"),
		),
	)
	if err != nil {
		slog.Error("aws config failed", "err", err)
		os.Exit(1)
	}

	var (
		dynamoEndpoint = envOr("DYNAMO_ENDPOINT", "http://localhost:8000")
		localstackURL  = envOr("LOCALSTACK_URL", "http://localhost:4566")
		redisAddr      = envOr("REDIS_ADDR", "localhost:6379")
	)

	// ── Bloco: DynamoDB Local ─────────────────────────────────────────────────
	products := dynamodb.New[Product]("products",
		dynamodb.WithAWSConfig(fakeCfg),
		dynamodb.WithTable("products-local"),
		dynamodb.WithPartitionKey("id"),
		dynamodb.WithEndpoint(dynamoEndpoint),
	)

	// ── Bloco: LocalStack S3 ──────────────────────────────────────────────────
	files := s3.New("files",
		s3.WithAWSConfig(fakeCfg),
		s3.WithBucket("local-bucket"),
		s3.WithKeyPrefix("dev/"),
		s3.WithEndpoint(localstackURL), // habilita path-style automaticamente
	)

	// ── Bloco: LocalStack SSM ─────────────────────────────────────────────────
	cfg := parameterstore.New("config",
		parameterstore.WithAWSConfig(fakeCfg),
		parameterstore.WithPathPrefix("/local/myapp"),
		parameterstore.WithEndpoint(localstackURL),
		parameterstore.WithDecryption(),
	)

	// ── Bloco: Redis local ────────────────────────────────────────────────────
	cache := redis.New("cache",
		redis.WithAddr(redisAddr),
		redis.WithKeyPrefix("local:"),
		redis.WithDialTimeout(2*time.Second),
	)

	app := core.NewContainer()
	app.MustRegister(products)
	app.MustRegister(files)
	app.MustRegister(cfg)
	app.MustRegister(cache)

	if err := app.InitAll(ctx); err != nil {
		slog.Error("init failed — verifique se os serviços locais estão rodando", "err", err)
		os.Exit(1)
	}
	defer app.ShutdownAll(ctx)

	slog.Info("ambiente local pronto",
		"dynamo", dynamoEndpoint,
		"localstack", localstackURL,
		"redis", redisAddr,
	)

	// ── DynamoDB Local ────────────────────────────────────────────────────────
	slog.Info("── DynamoDB Local ──")
	p := Product{
		ID:       "prd_001",
		Name:     "Teclado Mecânico",
		Price:    349.90,
		Category: "periféricos",
	}
	if err := products.PutItem(ctx, p); err != nil {
		slog.Error("PutItem", "err", err)
	} else {
		slog.Info("product saved", "id", p.ID)
	}

	fetched, err := products.GetItem(ctx, "prd_001", nil)
	if err != nil {
		slog.Error("GetItem", "err", err)
	} else {
		slog.Info("product fetched", "name", fetched.Name, "price", fetched.Price)
	}

	// ── LocalStack S3 ─────────────────────────────────────────────────────────
	slog.Info("── LocalStack S3 ──")
	appMeta := []byte(`{"env":"local","version":"0.1.0"}`)
	if err := files.PutObject(
		ctx,
		"config/app.json",
		bytes.NewReader(appMeta),
		s3.WithContentType("application/json"),
	); err != nil {
		slog.Error("PutObject", "err", err)
	} else {
		slog.Info("file uploaded to local S3")
	}

	objs, err := files.ListObjects(ctx, "")
	if err != nil {
		slog.Error("ListObjects", "err", err)
	} else {
		slog.Info("objects in local bucket", "count", len(objs))
		for _, o := range objs {
			slog.Info("  object", "key", o.Key, "size_bytes", o.Size)
		}
	}

	// ── LocalStack SSM ────────────────────────────────────────────────────────
	slog.Info("── LocalStack SSM ──")
	ssmParams := map[string]string{
		"db/host":                 "localhost",
		"db/port":                 "5432",
		"feature-flags/dark-mode": "true",
	}
	for name, value := range ssmParams {
		if err := cfg.PutParameter(ctx, name, value, types.ParameterTypeString, true); err != nil {
			slog.Error("PutParameter", "name", name, "err", err)
			continue
		}
		slog.Info("SSM param written", "name", name)
	}

	all, err := cfg.GetParametersByPath(ctx, "/")
	if err != nil {
		slog.Error("GetParametersByPath", "err", err)
	} else {
		slog.Info("all SSM params", "count", len(all))
		for k, v := range all {
			slog.Info("  param", "name", k, "value", v)
		}
	}

	// ── Redis ─────────────────────────────────────────────────────────────────
	slog.Info("── Redis ──")
	if err := cache.SetJSON(ctx, "product:prd_001", fetched, 5*time.Minute); err != nil {
		slog.Error("SetJSON", "err", err)
	} else {
		slog.Info("product cached in Redis")
	}

	var cached Product
	if err := cache.GetJSON(ctx, "product:prd_001", &cached); err != nil {
		slog.Error("GetJSON", "err", err)
	} else {
		slog.Info("product from Redis cache", "name", cached.Name, "price", cached.Price)
	}

	// Flags carregados do SSM armazenados em cache rápido via hash
	if err := cache.HSet(ctx, "flags",
		"dark-mode", "true",
		"new-checkout", "false",
	); err == nil {
		cache.Expire(ctx, "flags", 10*time.Minute)
		darkMode, _ := cache.HGet(ctx, "flags", "dark-mode")
		slog.Info("feature flag from cache", "dark-mode", darkMode)
	}

	slog.Info("local-dev sample complete ✓")
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
