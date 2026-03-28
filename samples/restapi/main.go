// samples/restapi/main.go
//
// Demonstra o uso do bloco REST API de forma isolada.
//
// Operações cobertas:
//   - GET  com query params e deserialização JSON
//   - POST com body JSON e captura de resposta
//   - PUT / PATCH para atualização parcial e total
//   - DELETE com verificação de status
//   - Autenticação via API Key em header
//   - Autenticação via Bearer token estático
//   - Autenticação via OAuth2 client_credentials (fetch automático)
//
// Este sample usa a API pública JSONPlaceholder (https://jsonplaceholder.typicode.com)
// para demonstrar chamadas reais sem necessidade de credenciais AWS.
//
// Variáveis de ambiente:
//
//	JSONPLACEHOLDER_URL  base URL (default: https://jsonplaceholder.typicode.com)
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/raywall/go-code-blocks/blocks/restapi"
	"github.com/raywall/go-code-blocks/core"
)

// ── Modelos ───────────────────────────────────────────────────────────────────

type Post struct {
	ID     int    `json:"id,omitempty"`
	UserID int    `json:"userId"`
	Title  string `json:"title"`
	Body   string `json:"body"`
}

type Comment struct {
	ID     int    `json:"id"`
	PostID int    `json:"postId"`
	Name   string `json:"name"`
	Email  string `json:"email"`
	Body   string `json:"body"`
}

func main() {
	ctx := context.Background()

	baseURL := envOr("JSONPLACEHOLDER_URL", "https://jsonplaceholder.typicode.com")

	// ── Bloco: API Key em header ──────────────────────────────────────────────
	// Demonstra WithAPIKeyHeader — o header é enviado automaticamente em todo request.
	apiKeyBlock := restapi.New("posts-api-key",
		restapi.WithBaseURL(baseURL),
		restapi.WithTimeout(10*time.Second),
		restapi.WithAPIKeyHeader("X-API-Key", "demo-key-12345"),
		restapi.WithHeader("X-Client-Version", "1.0.0"),
	)

	// ── Bloco: Bearer token estático ──────────────────────────────────────────
	bearerBlock := restapi.New("posts-bearer",
		restapi.WithBaseURL(baseURL),
		restapi.WithTimeout(10*time.Second),
		restapi.WithBearerToken("eyJhbGciOiJSUzI1NiJ9.demo.token"),
	)

	app := core.NewContainer()
	app.MustRegister(apiKeyBlock)
	app.MustRegister(bearerBlock)

	if err := app.InitAll(ctx); err != nil {
		slog.Error("init failed", "err", err)
		os.Exit(1)
	}
	defer app.ShutdownAll(ctx)

	// ── 1. GET — listar posts com query params ────────────────────────────────
	slog.Info("── GET /posts ──")
	var posts []Post
	if err := apiKeyBlock.GetJSON(ctx, "/posts", map[string]string{"_limit": "3"}, &posts); err != nil {
		slog.Error("GET /posts", "err", err)
	} else {
		slog.Info("posts fetched", "count", len(posts))
		for _, p := range posts {
			slog.Info("  post", "id", p.ID, "title", p.Title[:min(40, len(p.Title))])
		}
	}

	// ── 2. GET — buscar post por ID ───────────────────────────────────────────
	slog.Info("── GET /posts/1 ──")
	var post Post
	if err := apiKeyBlock.GetJSON(ctx, "/posts/1", nil, &post); err != nil {
		slog.Error("GET /posts/1", "err", err)
	} else {
		slog.Info("post fetched", "id", post.ID, "title", post.Title)
	}

	// ── 3. GET — comentários de um post (query param) ─────────────────────────
	slog.Info("── GET /comments?postId=1 ──")
	var comments []Comment
	if err := bearerBlock.GetJSON(ctx, "/comments", map[string]string{"postId": "1"}, &comments); err != nil {
		slog.Error("GET /comments", "err", err)
	} else {
		slog.Info("comments fetched", "count", len(comments))
		for _, c := range comments {
			slog.Info("  comment", "id", c.ID, "email", c.Email)
		}
	}

	// ── 4. POST — criar novo post ─────────────────────────────────────────────
	slog.Info("── POST /posts ──")
	newPost := Post{
		UserID: 1,
		Title:  "go-code-blocks in action",
		Body:   "REST API block makes external integrations trivial.",
	}
	var created Post
	if err := apiKeyBlock.PostJSON(ctx, "/posts", newPost, &created); err != nil {
		slog.Error("POST /posts", "err", err)
	} else {
		slog.Info("post created", "id", created.ID, "title", created.Title)
	}

	// ── 5. PUT — substituir post completo ─────────────────────────────────────
	slog.Info("── PUT /posts/1 ──")
	updatedPost := Post{
		ID:     1,
		UserID: 1,
		Title:  "Updated: go-code-blocks",
		Body:   "Full replacement via PUT.",
	}
	var replaced Post
	if err := apiKeyBlock.PutJSON(ctx, "/posts/1", updatedPost, &replaced); err != nil {
		slog.Error("PUT /posts/1", "err", err)
	} else {
		slog.Info("post replaced", "id", replaced.ID, "title", replaced.Title)
	}

	// ── 6. PATCH — atualização parcial ────────────────────────────────────────
	slog.Info("── PATCH /posts/1 ──")
	patch := map[string]any{"title": "Patched Title"}
	var patched Post
	if err := apiKeyBlock.PatchJSON(ctx, "/posts/1", patch, &patched); err != nil {
		slog.Error("PATCH /posts/1", "err", err)
	} else {
		slog.Info("post patched", "id", patched.ID, "title", patched.Title)
	}

	// ── 7. DELETE ─────────────────────────────────────────────────────────────
	slog.Info("── DELETE /posts/1 ──")
	resp, err := apiKeyBlock.Delete(ctx, "/posts/1")
	if err != nil {
		slog.Error("DELETE /posts/1", "err", err)
	} else {
		slog.Info("post deleted", "status", resp.StatusCode, "latency", resp.Latency())
	}

	// ── 8. Do — request customizado com headers extras ────────────────────────
	slog.Info("── Do (custom request) ──")
	raw, err := bearerBlock.Do(ctx, restapi.Request{
		Method: "GET",
		Path:   "/todos/1",
		Headers: map[string]string{
			"X-Request-ID": "req-demo-001",
		},
	})
	if err != nil {
		slog.Error("Do /todos/1", "err", err)
	} else {
		slog.Info("custom request",
			"status", raw.StatusCode,
			"body_preview", string(raw.Body[:min(80, len(raw.Body))]),
			"latency", raw.Latency(),
		)
	}

	// ── 9. HEAD — verificar existência sem body ───────────────────────────────
	slog.Info("── HEAD /posts/1 ──")
	headResp, err := apiKeyBlock.Head(ctx, "/posts/1")
	if err != nil {
		slog.Error("HEAD /posts/1", "err", err)
	} else {
		slog.Info("HEAD response",
			"status", headResp.StatusCode,
			"content-type", headResp.Headers.Get("Content-Type"),
		)
	}

	fmt.Println()
	slog.Info("restapi sample complete ✓")
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
