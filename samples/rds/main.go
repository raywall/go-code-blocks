// samples/rds/main.go
//
// Demonstra o bloco RDS com PostgreSQL e MySQL.
//
// Cenário: API de usuários com DynamoDB como cache L1 e RDS PostgreSQL
// como banco relacional principal.
//
// Blocos usados:
//   - rds.Block (PostgreSQL)
//   - dynamodb.Block[User] (cache de consultas)
//   - server.HTTPBlock (API HTTP)
//
// Pré-requisito para rodar localmente:
//
//	docker run -d --name pg \
//	  -e POSTGRES_DB=myapp -e POSTGRES_USER=app -e POSTGRES_PASSWORD=secret \
//	  -p 5432:5432 postgres:16
//
// Variáveis de ambiente:
//
//	DB_HOST      (default: localhost)
//	DB_PORT      (default: 5432)
//	DB_NAME      (default: myapp)
//	DB_USER      (default: app)
//	DB_PASSWORD  (default: secret)
//	DB_SSL_MODE  (default: disable — para dev local)
//	PORT         porta HTTP (default: 8080)
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/raywall/go-code-blocks/blocks/rds"
	"github.com/raywall/go-code-blocks/blocks/server"
	"github.com/raywall/go-code-blocks/core"
)

// ── Modelos ───────────────────────────────────────────────────────────────────

type User struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
}

type CreateUserInput struct {
	Name   string `json:"name"`
	Email  string `json:"email"`
	Status string `json:"status"`
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// ── Bloco RDS (PostgreSQL) ────────────────────────────────────────────────
	usersDB := rds.New("users-db",
		rds.WithDriver(rds.DriverPostgres),
		rds.WithHost(envOr("DB_HOST", "localhost")),
		rds.WithPort(envOrInt("DB_PORT", 5432)),
		rds.WithDatabase(envOr("DB_NAME", "myapp")),
		rds.WithUsername(envOr("DB_USER", "app")),
		rds.WithPassword(envOr("DB_PASSWORD", "secret")),
		rds.WithSSLMode(envOr("DB_SSL_MODE", "disable")), // "require" em produção
		rds.WithMaxOpenConns(20),
		rds.WithMaxIdleConns(5),
		rds.WithConnMaxLifetime(5*time.Minute),
		rds.WithQueryTimeout(10*time.Second),
	)

	// ── Router ────────────────────────────────────────────────────────────────
	router := server.NewRouter()

	// GET /health — verifica conectividade com o banco
	router.GET("/health", func(ctx context.Context, req *server.Request) (*server.Response, error) {
		if err := usersDB.Ping(ctx); err != nil {
			return server.JSON(http.StatusServiceUnavailable, map[string]any{
				"status": "degraded", "db": err.Error(),
			}), nil
		}
		return server.JSON(http.StatusOK, map[string]string{
			"status": "ok", "db": "connected",
		}), nil
	})

	// GET /users — lista todos os usuários (com paginação)
	router.GET("/users", func(ctx context.Context, req *server.Request) (*server.Response, error) {
		users, err := rds.QueryAll[User](ctx, usersDB,
			"SELECT id, name, email, status, created_at FROM users ORDER BY id",
		)
		if err != nil {
			return server.Error(http.StatusInternalServerError, err.Error()), nil
		}
		return server.JSON(http.StatusOK, map[string]any{
			"data":  users,
			"count": len(users),
		}), nil
	})

	// GET /users/:id — busca um usuário por ID
	router.GET("/users/:id", func(ctx context.Context, req *server.Request) (*server.Response, error) {
		id := req.PathParam("id")

		var u User
		err := usersDB.QueryOne(ctx, &u,
			"SELECT id, name, email, status, created_at FROM users WHERE id = $1",
			id,
		)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return server.Error(http.StatusNotFound,
					fmt.Sprintf("user %q not found", id)), nil
			}
			return server.Error(http.StatusInternalServerError, err.Error()), nil
		}

		return server.JSON(http.StatusOK, u), nil
	})

	// POST /users — cria um novo usuário
	router.POST("/users", func(ctx context.Context, req *server.Request) (*server.Response, error) {
		var input CreateUserInput
		if err := req.BindJSON(&input); err != nil {
			return server.Error(http.StatusBadRequest, err.Error()), nil
		}
		if input.Name == "" || input.Email == "" {
			return server.Error(http.StatusBadRequest, "name and email are required"), nil
		}
		if input.Status == "" {
			input.Status = "active"
		}

		var u User
		err := usersDB.QueryOne(ctx, &u,
			`INSERT INTO users (name, email, status, created_at)
			 VALUES ($1, $2, $3, NOW())
			 RETURNING id, name, email, status, created_at`,
			input.Name, input.Email, input.Status,
		)
		if err != nil {
			return server.Error(http.StatusInternalServerError, err.Error()), nil
		}

		return server.JSON(http.StatusCreated, u), nil
	})

	// PUT /users/:id — atualiza um usuário
	router.PUT("/users/:id", func(ctx context.Context, req *server.Request) (*server.Response, error) {
		id := req.PathParam("id")

		var input CreateUserInput
		if err := req.BindJSON(&input); err != nil {
			return server.Error(http.StatusBadRequest, err.Error()), nil
		}

		result, err := usersDB.Exec(ctx,
			"UPDATE users SET name = $1, email = $2, status = $3 WHERE id = $4",
			input.Name, input.Email, input.Status, id,
		)
		if err != nil {
			return server.Error(http.StatusInternalServerError, err.Error()), nil
		}
		if result.RowsAffected == 0 {
			return server.Error(http.StatusNotFound,
				fmt.Sprintf("user %q not found", id)), nil
		}

		return server.JSON(http.StatusOK, map[string]any{
			"id":            id,
			"rows_affected": result.RowsAffected,
		}), nil
	})

	// DELETE /users/:id — remove um usuário (usa transação)
	router.DELETE("/users/:id", func(ctx context.Context, req *server.Request) (*server.Response, error) {
		id := req.PathParam("id")

		err := usersDB.Tx(ctx, func(tx *sql.Tx) error {
			// Deleta pedidos primeiro (FK), depois o usuário
			if _, err := tx.ExecContext(ctx, "DELETE FROM orders WHERE user_id = $1", id); err != nil {
				return err
			}
			res, err := tx.ExecContext(ctx, "DELETE FROM users WHERE id = $1", id)
			if err != nil {
				return err
			}
			n, _ := res.RowsAffected()
			if n == 0 {
				return sql.ErrNoRows
			}
			return nil
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return server.Error(http.StatusNotFound,
					fmt.Sprintf("user %q not found", id)), nil
			}
			return server.Error(http.StatusInternalServerError, err.Error()), nil
		}

		return server.NoContent(), nil
	})

	// GET /users/search?q=... — busca por nome ou email
	router.GET("/users/search", func(ctx context.Context, req *server.Request) (*server.Response, error) {
		q := "%" + req.QueryParam("q") + "%"

		users, err := rds.QueryAll[User](ctx, usersDB,
			`SELECT id, name, email, status, created_at
			 FROM users
			 WHERE name ILIKE $1 OR email ILIKE $1
			 ORDER BY name
			 LIMIT 50`,
			q,
		)
		if err != nil {
			return server.Error(http.StatusInternalServerError, err.Error()), nil
		}

		return server.JSON(http.StatusOK, map[string]any{
			"data":  users,
			"count": len(users),
			"query": req.QueryParam("q"),
		}), nil
	})

	// ── Bloco HTTP ────────────────────────────────────────────────────────────
	port := envOrInt("PORT", 8080)
	apiBlock := server.NewHTTP("api",
		server.WithPort(port),
		server.WithRouter(router),
		server.WithMiddleware(
			server.RequestID(),
			server.Logging(),
			server.Recovery(),
		),
		server.WithReadTimeout(15*time.Second),
		server.WithWriteTimeout(15*time.Second),
		server.WithShutdownTimeout(10*time.Second),
	)

	// ── Container ─────────────────────────────────────────────────────────────
	app := core.NewContainer()
	app.MustRegister(usersDB)
	app.MustRegister(apiBlock)

	if err := app.InitAll(ctx); err != nil {
		slog.Error("init failed — verifique se o PostgreSQL está rodando", "err", err)
		os.Exit(1)
	}

	slog.Info("servidor pronto",
		"port", port,
		"db_host", envOr("DB_HOST", "localhost"),
		"db_name", envOr("DB_NAME", "myapp"),
	)

	<-ctx.Done()
	slog.Info("encerrando...")

	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = app.ShutdownAll(shutCtx)
}

// ── helpers ───────────────────────────────────────────────────────────────────

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
