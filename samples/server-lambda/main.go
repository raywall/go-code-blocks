// samples/server-lambda/main.go
//
// Demonstra o bloco Lambda com API Gateway v2 (HTTP API) e ALB.
// O mesmo Router e os mesmos Handlers funcionam com ambas as origens —
// basta trocar WithSource() para alternar entre elas.
//
// O handler é idêntico ao do servidor HTTP; apenas o ponto de entrada muda.
//
// Deploy:
//
//	GOOS=linux GOARCH=amd64 go build -tags lambda.norpc -o bootstrap .
//	zip function.zip bootstrap
//	aws lambda update-function-code --function-name my-api --zip-file fileb://function.zip
//
// Variáveis de ambiente (configuradas no Lambda console ou Terraform):
//
//	LAMBDA_SOURCE   "apigateway_v2" | "apigateway_v1" | "alb"  (default: apigateway_v2)
//	AWS_REGION      região AWS (configurada automaticamente pelo runtime)
//	USERS_TABLE     nome da tabela DynamoDB
//	REDIS_ADDR      endpoint do Redis (ElastiCache)
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/raywall/go-code-blocks/blocks/decision"
	"github.com/raywall/go-code-blocks/blocks/dynamodb"
	"github.com/raywall/go-code-blocks/blocks/redis"
	"github.com/raywall/go-code-blocks/blocks/server"
	"github.com/raywall/go-code-blocks/core"
)

// ── Modelos ───────────────────────────────────────────────────────────────────

type User struct {
	ID           string `dynamodbav:"id"            json:"id"`
	Name         string `dynamodbav:"name"          json:"name"`
	Email        string `dynamodbav:"email"         json:"email"`
	CustomerType string `dynamodbav:"customer_type" json:"customer_type" decision:"customer_type"`
	CreatedAt    string `dynamodbav:"created_at"    json:"created_at"`
}

// ── Globals inicializados via init ────────────────────────────────────────────
// Em Lambda, o container é inicializado fora do handler para reutilizar
// conexões entre invocações (warm start).

var (
	app         *core.Container
	lambdaBlock *server.LambdaBlock
)

func init() {
	ctx := context.Background()

	// ── Resolve source do evento ──────────────────────────────────────────────
	sourceStr := envOr("LAMBDA_SOURCE", "apigateway_v2")
	var src server.Source
	switch sourceStr {
	case "apigateway_v1":
		src = server.SourceAPIGatewayV1
	case "alb":
		src = server.SourceALB
	default:
		src = server.SourceAPIGatewayV2
	}

	// ── Blocos de integração ──────────────────────────────────────────────────
	usersDB := dynamodb.New[User]("users-db",
		dynamodb.WithRegion(envOr("AWS_REGION", "us-east-1")),
		dynamodb.WithTable(envOr("USERS_TABLE", "users-prod")),
		dynamodb.WithPartitionKey("id"),
	)

	// Em Lambda + ElastiCache, Redis só é acessível dentro da VPC.
	// O bloco falha no Init se não estiver acessível, o que é o comportamento correto.
	cacheBlock := redis.New("cache",
		redis.WithAddr(envOr("REDIS_ADDR", "localhost:6379")),
		redis.WithKeyPrefix("lambda:users:"),
		redis.WithDialTimeout(1*time.Second), // timeout curto em cold start
	)

	validator := decision.New("validator",
		decision.WithRule("pj-only",
			`customer_type == "PJ"`,
			decision.Schema{"customer_type": decision.String},
		),
	)

	// ── Router — idêntico ao sample HTTP ─────────────────────────────────────
	router := buildRouter(usersDB, cacheBlock, validator)

	// ── Bloco Lambda ──────────────────────────────────────────────────────────
	lambdaBlock = server.NewLambda("api",
		server.WithSource(src),
		server.WithRouter(router),
		server.WithMiddleware(
			server.RequestID(),
			server.Logging(),
			server.Recovery(),
			server.CORS(server.CORSConfig{
				AllowOrigins: []string{"https://app.example.com"},
				AllowMethods: []string{"GET", "POST", "DELETE", "OPTIONS"},
			}),
		),
	)

	// ── Container ─────────────────────────────────────────────────────────────
	app = core.NewContainer()
	app.MustRegister(usersDB)
	app.MustRegister(cacheBlock)
	app.MustRegister(validator)
	app.MustRegister(lambdaBlock)

	if err := app.InitAll(ctx); err != nil {
		slog.Error("lambda init failed", "err", err)
		os.Exit(1)
	}

	slog.Info("lambda initialized",
		"source", string(src),
		"table", envOr("USERS_TABLE", "users-prod"),
	)
}

func main() {
	// Transfere o controle ao runtime Lambda.
	// lambda.Start() bloca até o ambiente ser encerrado.
	lambdaBlock.Start()
}

// ── Router ────────────────────────────────────────────────────────────────────

// buildRouter constrói o mesmo router usado no sample HTTP, demonstrando
// que os handlers são completamente agnósticos ao transporte.
func buildRouter(
	usersDB *dynamodb.Block[User],
	cacheBlock *redis.Block,
	validator *decision.Block,
) *server.Router {
	router := server.NewRouter()

	router.GET("/health", func(ctx context.Context, req *server.Request) (*server.Response, error) {
		return server.JSON(http.StatusOK, map[string]any{
			"status":    "ok",
			"source":    string(req.Source),
			"stage":     req.Stage,
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		}), nil
	})

	router.GET("/users/:id", func(ctx context.Context, req *server.Request) (*server.Response, error) {
		id := req.PathParam("id")

		// Cache-aside pattern — igual ao HTTP server
		var u User
		if err := cacheBlock.GetJSON(ctx, "profile:"+id, &u); err == nil {
			return server.JSON(http.StatusOK, map[string]any{"data": u, "source": "cache"}), nil
		}

		user, err := usersDB.GetItem(ctx, id, nil)
		if err != nil {
			if err == core.ErrItemNotFound {
				return server.Error(http.StatusNotFound, fmt.Sprintf("user %q not found", id)), nil
			}
			return server.Error(http.StatusInternalServerError, err.Error()), nil
		}

		_ = cacheBlock.SetJSON(ctx, "profile:"+id, user, 5*time.Minute)
		return server.JSON(http.StatusOK, map[string]any{"data": user, "source": "dynamodb"}), nil
	})

	router.POST("/users", func(ctx context.Context, req *server.Request) (*server.Response, error) {
		var u User
		if err := req.BindJSON(&u); err != nil {
			return server.Error(http.StatusBadRequest, err.Error()), nil
		}

		// Validação via CEL
		result, err := validator.EvaluateFrom(ctx, "pj-only", u)
		if err != nil {
			return server.Error(http.StatusInternalServerError, err.Error()), nil
		}
		if !result {
			return server.Error(http.StatusForbidden,
				fmt.Sprintf("customer_type %q not accepted", u.CustomerType)), nil
		}

		u.CreatedAt = time.Now().UTC().Format(time.RFC3339)
		if err := usersDB.PutItem(ctx, u); err != nil {
			return server.Error(http.StatusInternalServerError, err.Error()), nil
		}
		return server.JSON(http.StatusCreated, u), nil
	})

	router.DELETE("/users/:id", func(ctx context.Context, req *server.Request) (*server.Response, error) {
		id := req.PathParam("id")
		if err := usersDB.DeleteItem(ctx, id, nil); err != nil {
			return server.Error(http.StatusInternalServerError, err.Error()), nil
		}
		_ = cacheBlock.Delete(ctx, "profile:"+id)
		return server.NoContent(), nil
	})

	return router
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
