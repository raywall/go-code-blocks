// samples/server-http/main.go
//
// Demonstra o bloco de servidor HTTP com roteamento, middleware e integração
// com os demais blocos (DynamoDB, Redis, bloco de decisão).
//
// Endpoints expostos:
//
//	GET  /health                    → health check
//	GET  /users/:id                 → busca usuário (Redis cache → DynamoDB)
//	POST /users                     → cria usuário (CEL valida tipo PJ/PF)
//	GET  /users/:id/orders          → lista pedidos do usuário
//	DELETE /users/:id               → remove usuário + invalida cache
//
// Variáveis de ambiente:
//
//	PORT            porta HTTP (default: 8080)
//	AWS_REGION      região AWS (default: us-east-1)
//	USERS_TABLE     tabela DynamoDB (default: users-dev)
//	REDIS_ADDR      endereço Redis (default: localhost:6379)
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
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

// ── Bloco de decisão para validação de entrada ────────────────────────────────

const (
	blockUsers    = "users-db"
	blockCache    = "cache"
	blockDecision = "validator"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// ── Blocos de integração ──────────────────────────────────────────────────
	usersDB := dynamodb.New[User](blockUsers,
		dynamodb.WithRegion(envOr("AWS_REGION", "us-east-1")),
		dynamodb.WithTable(envOr("USERS_TABLE", "users-dev")),
		dynamodb.WithPartitionKey("id"),
	)

	cacheBlock := redis.New(blockCache,
		redis.WithAddr(envOr("REDIS_ADDR", "localhost:6379")),
		redis.WithKeyPrefix("users:"),
		redis.WithDialTimeout(2*time.Second),
	)

	validator := decision.New(blockDecision,
		decision.WithRule("pj-only",
			`customer_type == "PJ"`,
			decision.Schema{"customer_type": decision.String},
		),
	)

	// ── Router ────────────────────────────────────────────────────────────────
	router := server.NewRouter()

	// Middleware global: logging + recovery + CORS + request-id
	// (registrado no WithMiddleware do bloco, não no router, para que
	//  seja aplicado inclusive a rotas não encontradas)

	router.GET("/health", func(ctx context.Context, req *server.Request) (*server.Response, error) {
		return server.JSON(http.StatusOK, map[string]string{
			"status":    "ok",
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		}), nil
	})

	router.GET("/users/:id", func(ctx context.Context, req *server.Request) (*server.Response, error) {
		id := req.PathParam("id")

		// Tenta cache primeiro
		var u User
		cacheKey := "profile:" + id
		if err := cacheBlock.GetJSON(ctx, cacheKey, &u); err == nil {
			return server.JSON(http.StatusOK, map[string]any{
				"data":   u,
				"source": "cache",
			}), nil
		}

		// Fallback para DynamoDB
		user, err := usersDB.GetItem(ctx, id, nil)
		if err != nil {
			if err == core.ErrItemNotFound {
				return server.Error(http.StatusNotFound, fmt.Sprintf("user %q not found", id)), nil
			}
			return server.Error(http.StatusInternalServerError, err.Error()), nil
		}

		// Popula cache
		_ = cacheBlock.SetJSON(ctx, cacheKey, user, 10*time.Minute)

		return server.JSON(http.StatusOK, map[string]any{
			"data":   user,
			"source": "dynamodb",
		}), nil
	})

	router.POST("/users", func(ctx context.Context, req *server.Request) (*server.Response, error) {
		var u User
		if err := req.BindJSON(&u); err != nil {
			return server.Error(http.StatusBadRequest, err.Error()), nil
		}
		if u.ID == "" || u.Name == "" || u.Email == "" || u.CustomerType == "" {
			return server.Error(http.StatusBadRequest, "id, name, email, customer_type are required"), nil
		}

		// Valida tipo de cliente via CEL
		result, err := validator.EvaluateFrom(ctx, "pj-only", u)
		if err != nil {
			return server.Error(http.StatusInternalServerError, err.Error()), nil
		}
		if !result {
			return server.Error(http.StatusForbidden,
				fmt.Sprintf("customer_type %q not accepted in this channel", u.CustomerType)), nil
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

		// Invalida cache
		_ = cacheBlock.Delete(ctx, "profile:"+id)

		return server.NoContent(), nil
	})

	router.GET("/users/:id/orders", func(ctx context.Context, req *server.Request) (*server.Response, error) {
		id := req.PathParam("id")
		// Placeholder — em produção buscaria de uma tabela de pedidos
		return server.JSON(http.StatusOK, map[string]any{
			"user_id": id,
			"orders":  []any{},
		}), nil
	})

	// ── Bloco HTTP com middleware ──────────────────────────────────────────────
	port := envOrInt("PORT", 8080)

	httpBlock := server.NewHTTP("api",
		server.WithPort(port),
		server.WithRouter(router),
		server.WithMiddleware(
			server.RequestID(),
			server.Logging(),
			server.Recovery(),
			server.CORS(server.CORSConfig{}),
		),
		server.WithReadTimeout(15*time.Second),
		server.WithWriteTimeout(15*time.Second),
		server.WithShutdownTimeout(10*time.Second),
	)

	// ── Container ─────────────────────────────────────────────────────────────
	app := core.NewContainer()
	app.MustRegister(usersDB)
	app.MustRegister(cacheBlock)
	app.MustRegister(validator)
	app.MustRegister(httpBlock)

	if err := app.InitAll(ctx); err != nil {
		slog.Error("init failed", "err", err)
		os.Exit(1)
	}

	slog.Info("server listening",
		"port", port,
		"endpoints", []string{
			"GET  /health",
			"GET  /users/:id",
			"POST /users",
			"DELETE /users/:id",
			"GET  /users/:id/orders",
		},
	)

	// Aguarda sinal de encerramento
	<-ctx.Done()
	slog.Info("shutdown signal received")

	// ShutdownAll chama httpBlock.Shutdown (graceful) + demais blocos
	shutCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := app.ShutdownAll(shutCtx); err != nil {
		slog.Error("shutdown error", "err", err)
	}
	slog.Info("server stopped")
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envOrInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	var n int
	fmt.Sscanf(v, "%d", &n)
	if n == 0 {
		return fallback
	}
	return n
}
