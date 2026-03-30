// samples/restapi-pipeline/main.go
//
// Demonstra execução concorrente e sequencial de chamadas HTTP usando
// FanOut e Pipeline com dependências (DAG).
//
// Cenário: montar um resumo de pedido de e-commerce
//
// Grafo de dependências:
//
//	Wave 0 (paralelo, sem dependências):
//	  ├── "user"     → GET /users/:id
//	  ├── "catalog"  → GET /posts?limit=5
//	  └── "rates"    → GET /todos?limit=3
//
//	Wave 1 (paralelo, dependem de "user"):
//	  ├── "orders"   → GET /posts?userId=:user.id
//	  └── "payments" → GET /comments?postId=:user.id
//
//	Wave 2 (depende de "orders" + "catalog"):
//	  └── "summary"  → POST /posts (payload enriquecido)
//
// Resultado: 3 ondas sequenciais, cada onda 100% paralela internamente.
// Tempo total ≈ max(wave0) + max(wave1) + max(wave2) — em vez de soma de todos.
//
// Usa JSONPlaceholder como backend real para chamadas de rede verdadeiras.
//
// Variáveis de ambiente:
//
//	API_BASE_URL  (default: https://jsonplaceholder.typicode.com)
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/raywall/go-code-blocks/blocks/restapi"
	"github.com/raywall/go-code-blocks/core"
)

// ── Modelos ───────────────────────────────────────────────────────────────────

type User struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Username string `json:"username"`
	Email    string `json:"email"`
}

type Post struct {
	ID     int    `json:"id"`
	UserID int    `json:"userId"`
	Title  string `json:"title"`
	Body   string `json:"body"`
}

type Comment struct {
	ID     int    `json:"id"`
	PostID int    `json:"postId"`
	Email  string `json:"email"`
	Name   string `json:"name"`
}

func main() {
	ctx := context.Background()

	baseURL := envOr("API_BASE_URL", "https://jsonplaceholder.typicode.com")

	api := restapi.New("api",
		restapi.WithBaseURL(baseURL),
		restapi.WithTimeout(15*time.Second),
	)

	app := core.NewContainer()
	app.MustRegister(api)
	if err := app.InitAll(ctx); err != nil {
		slog.Error("init failed", "err", err)
		os.Exit(1)
	}
	defer app.ShutdownAll(ctx)

	// ──────────────────────────────────────────────────────────────────────────
	// Exemplo 1: FanOut — N requisições totalmente independentes em paralelo
	// ──────────────────────────────────────────────────────────────────────────
	fmt.Println()
	slog.Info("═══ Exemplo 1: FanOut — 4 requests em paralelo ═══")

	start := time.Now()
	fanResults, err := api.FanOut(ctx, map[string]restapi.Request{
		"user_1": {Path: "/users/1"},
		"user_2": {Path: "/users/2"},
		"user_3": {Path: "/users/3"},
		"user_4": {Path: "/users/4"},
	})
	elapsed := time.Since(start)

	if err != nil {
		slog.Error("FanOut failed", "err", err)
	} else {
		slog.Info("FanOut concluído",
			"wall_time", elapsed.Round(time.Millisecond),
			"requests", 4,
		)
		for _, name := range []string{"user_1", "user_2", "user_3", "user_4"} {
			sr := fanResults.Get(name)
			if sr.OK() {
				var u User
				_ = fanResults.JSON(name, &u)
				slog.Info("  →", "step", name, "user", u.Name, "latency", sr.Latency.Round(time.Millisecond))
			}
		}
	}

	// ──────────────────────────────────────────────────────────────────────────
	// Exemplo 2: Pipeline DAG completo — 3 ondas, máxima paralelização
	// ──────────────────────────────────────────────────────────────────────────
	fmt.Println()
	slog.Info("═══ Exemplo 2: Pipeline DAG (3 ondas) ═══")
	slog.Info("Grafo: [user, catalog, rates] → [orders, payments] → [summary]")

	const targetUserID = 1

	steps := []restapi.PipelineStep{
		// ── Wave 0: independentes ─────────────────────────────────────────────
		{
			Name: "user",
			Build: func(ctx context.Context, _ *restapi.Results) (restapi.Request, error) {
				return restapi.Request{
					Path: fmt.Sprintf("/users/%d", targetUserID),
				}, nil
			},
		},
		{
			Name: "catalog",
			Build: func(ctx context.Context, _ *restapi.Results) (restapi.Request, error) {
				return restapi.Request{
					Path:  "/posts",
					Query: map[string]string{"_limit": "5"},
				}, nil
			},
		},
		{
			Name: "rates",
			Build: func(ctx context.Context, _ *restapi.Results) (restapi.Request, error) {
				return restapi.Request{
					Path:  "/todos",
					Query: map[string]string{"_limit": "3"},
				}, nil
			},
		},

		// ── Wave 1: dependem de "user", paralelos entre si ────────────────────
		{
			Name:      "orders",
			DependsOn: []string{"user"},
			Build: func(ctx context.Context, prev *restapi.Results) (restapi.Request, error) {
				var u User
				if err := prev.JSON("user", &u); err != nil {
					return restapi.Request{}, fmt.Errorf("orders: precisa de user: %w", err)
				}
				return restapi.Request{
					Path:  "/posts",
					Query: map[string]string{"userId": fmt.Sprintf("%d", u.ID), "_limit": "4"},
				}, nil
			},
		},
		{
			Name:      "payments",
			DependsOn: []string{"user"},
			Build: func(ctx context.Context, prev *restapi.Results) (restapi.Request, error) {
				var u User
				if err := prev.JSON("user", &u); err != nil {
					return restapi.Request{}, fmt.Errorf("payments: precisa de user: %w", err)
				}
				return restapi.Request{
					Path:  "/comments",
					Query: map[string]string{"postId": fmt.Sprintf("%d", u.ID), "_limit": "3"},
				}, nil
			},
		},

		// ── Wave 2: depende de "orders" + "catalog" ───────────────────────────
		{
			Name:      "summary",
			DependsOn: []string{"orders", "catalog"},
			Build: func(ctx context.Context, prev *restapi.Results) (restapi.Request, error) {
				var orders []Post
				if err := prev.JSON("orders", &orders); err != nil {
					return restapi.Request{}, fmt.Errorf("summary: precisa de orders: %w", err)
				}
				var catalog []Post
				if err := prev.JSON("catalog", &catalog); err != nil {
					return restapi.Request{}, fmt.Errorf("summary: precisa de catalog: %w", err)
				}

				orderIDs := make([]int, 0, len(orders))
				for _, o := range orders {
					orderIDs = append(orderIDs, o.ID)
				}

				return restapi.Request{
					Method: "POST",
					Path:   "/posts",
					Body: map[string]any{
						"title":         "order-summary",
						"order_count":   len(orders),
						"catalog_count": len(catalog),
						"order_ids":     orderIDs,
					},
				}, nil
			},
		},
	}

	start = time.Now()
	results, err := api.Pipeline(ctx, steps,
		restapi.WithContinueOnError(),
		restapi.WithMaxConcurrency(5),
	)
	elapsed = time.Since(start)

	if err != nil {
		slog.Warn("pipeline concluído com erros", "err", err)
	} else {
		slog.Info("Pipeline concluído", "wall_time", elapsed.Round(time.Millisecond))
	}

	// ── Relatório de performance ──────────────────────────────────────────────
	fmt.Println()
	printTable(results, steps)

	// ── Usar os dados coletados ───────────────────────────────────────────────
	fmt.Println()
	slog.Info("═══ Resultados ═══")

	var user User
	if err := results.JSON("user", &user); err == nil {
		slog.Info("Usuário", "nome", user.Name, "email", user.Email)
	}

	var orders []Post
	if err := results.JSON("orders", &orders); err == nil {
		slog.Info("Pedidos", "total", len(orders))
		for _, o := range orders {
			slog.Info("  →", "id", o.ID, "título", shorten(o.Title, 45))
		}
	}

	var payments []Comment
	if err := results.JSON("payments", &payments); err == nil {
		slog.Info("Pagamentos", "registros", len(payments))
	}

	if sr := results.Get("summary"); sr != nil && sr.OK() {
		slog.Info("Summary criado", "http", sr.Response.StatusCode, "latência", sr.Latency.Round(time.Millisecond))
	}

	// ──────────────────────────────────────────────────────────────────────────
	// Exemplo 3: FanOut com MaxConcurrency para respeitar rate limits
	// ──────────────────────────────────────────────────────────────────────────
	fmt.Println()
	slog.Info("═══ Exemplo 3: FanOut com MaxConcurrency=2 (rate limit) ═══")
	slog.Info("10 requests, máximo 2 em paralelo simultaneamente")

	requests := make(map[string]restapi.Request, 10)
	for i := 1; i <= 10; i++ {
		requests[fmt.Sprintf("user_%d", i)] = restapi.Request{
			Path: fmt.Sprintf("/users/%d", i),
		}
	}

	start = time.Now()
	rateLimitedResults, err := api.FanOut(ctx, requests,
		restapi.WithMaxConcurrency(2), // nunca mais de 2 conexões simultâneas
	)
	elapsed = time.Since(start)

	if err != nil {
		slog.Error("rate-limited FanOut failed", "err", err)
	} else {
		ok := 0
		for _, name := range rateLimitedResults.Names() {
			if rateLimitedResults.Get(name).OK() {
				ok++
			}
		}
		slog.Info("Rate-limited FanOut concluído",
			"wall_time", elapsed.Round(time.Millisecond),
			"requests", 10,
			"succeeded", ok,
			"max_concurrent", 2,
		)
	}

	// ──────────────────────────────────────────────────────────────────────────
	// Exemplo 4: Pipeline com WaveTimeout
	// ──────────────────────────────────────────────────────────────────────────
	fmt.Println()
	slog.Info("═══ Exemplo 4: Pipeline com WaveTimeout de 10s ═══")

	timeoutSteps := []restapi.PipelineStep{
		{
			Name: "a",
			Build: func(ctx context.Context, _ *restapi.Results) (restapi.Request, error) {
				return restapi.Request{Path: "/users/1"}, nil
			},
		},
		{
			Name: "b",
			Build: func(ctx context.Context, _ *restapi.Results) (restapi.Request, error) {
				return restapi.Request{Path: "/users/2"}, nil
			},
		},
		{
			Name:      "c",
			DependsOn: []string{"a", "b"},
			Build: func(ctx context.Context, prev *restapi.Results) (restapi.Request, error) {
				var ua, ub User
				_ = prev.JSON("a", &ua)
				_ = prev.JSON("b", &ub)
				return restapi.Request{
					Path:  "/posts",
					Query: map[string]string{"_limit": "2"},
				}, nil
			},
		},
	}

	start = time.Now()
	_, pErr := api.Pipeline(ctx, timeoutSteps, restapi.WithWaveTimeout(10*time.Second))
	elapsed = time.Since(start)
	if pErr != nil {
		slog.Warn("pipeline com timeout falhou", "err", pErr)
	} else {
		slog.Info("pipeline com timeout OK", "elapsed", elapsed.Round(time.Millisecond))
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func printTable(results *restapi.Results, steps []restapi.PipelineStep) {
	fmt.Printf("%-12s  %-8s  %-10s  %s\n", "STEP", "STATUS", "LATENCY", "HTTP")
	fmt.Println(strings.Repeat("─", 50))
	for _, s := range steps {
		sr := results.Get(s.Name)
		if sr == nil {
			fmt.Printf("%-12s  %-8s\n", s.Name, "skipped")
			continue
		}
		status := "✓ ok"
		code := ""
		if sr.Err != nil {
			status = "✗ error"
		} else if sr.Response != nil {
			code = fmt.Sprintf("HTTP %d", sr.Response.StatusCode)
		}
		fmt.Printf("%-12s  %-8s  %-10s  %s\n",
			s.Name, status, sr.Latency.Round(time.Millisecond), code)
	}
}

func shorten(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
