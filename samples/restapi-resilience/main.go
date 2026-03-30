// samples/restapi-resilience/main.go
//
// Demonstra os dois comportamentos de resiliência do Pipeline:
//
//  1. CASCADE ABORT — quando um step essencial falha, todos os steps que
//     dependem dele (direta ou transitivamente) são abortados automaticamente.
//     Nenhuma chamada HTTP é feita para dependentes de um step falho.
//
//  2. RETRY com backoff — steps transientes que retornam 5xx ou erros de rede
//     são reexecutados automaticamente, com delay configurável e backoff
//     exponencial. O retry pode ser definido por step ou como default do pipeline.
//
// Cenário: pipeline de checkout
//
//	Wave 0:  [token, user-profile]          — independentes
//	Wave 1:  [cart, address]                — dependem de user-profile
//	Wave 2:  [shipping-calc, tax-calc]      — dependem de cart + address
//	Wave 3:  [checkout-confirm]             — depende de shipping-calc + tax-calc
//
//	Se user-profile falhar:
//	  → cart, address são skipped
//	  → shipping-calc, tax-calc são skipped
//	  → checkout-confirm é skipped
//	  A raiz do problema aparece em apenas 1 step falho, não em 4.
//
// Este sample usa um servidor HTTP local simples para simular falhas e retries
// sem depender de serviços externos.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"time"

	"github.com/raywall/go-code-blocks/blocks/restapi"
	"github.com/raywall/go-code-blocks/core"
)

// ── Modelos ───────────────────────────────────────────────────────────────────

type UserProfile struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

type Cart struct {
	Items []string `json:"items"`
	Total float64  `json:"total"`
}

type Address struct {
	Street string `json:"street"`
	City   string `json:"city"`
	State  string `json:"state"`
}

type ShippingRate struct {
	Carrier string  `json:"carrier"`
	Price   float64 `json:"price"`
}

type TaxResult struct {
	Rate   float64 `json:"rate"`
	Amount float64 `json:"amount"`
}

// ── Servidor local simulado ───────────────────────────────────────────────────

// flakyCounter rastreia quantas chamadas cada endpoint recebeu,
// permitindo simular falhas nas primeiras N tentativas.
type flakyCounter struct {
	counts map[string]*atomic.Int32
}

func newFlakyCounter(paths ...string) *flakyCounter {
	fc := &flakyCounter{counts: make(map[string]*atomic.Int32)}
	for _, p := range paths {
		var c atomic.Int32
		fc.counts[p] = &c
	}
	return fc
}

func (fc *flakyCounter) call(path string) int {
	c, ok := fc.counts[path]
	if !ok {
		return 1
	}
	return int(c.Add(1))
}

func buildTestServer(fc *flakyCounter, userProfileFails bool) *httptest.Server {
	mux := http.NewServeMux()

	// /token — sempre ok
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"access_token":"tok_abc123","expires_in":3600}`)
	})

	// /user-profile — falha controlada para testar cascade abort
	mux.HandleFunc("/user-profile", func(w http.ResponseWriter, r *http.Request) {
		if userProfileFails {
			w.WriteHeader(http.StatusInternalServerError)
			io.WriteString(w, `{"error":"database unavailable"}`)
			return
		}
		io.WriteString(w, `{"id":"usr_001","name":"Alice","email":"alice@example.com"}`)
	})

	// /cart — flaky: falha nas 2 primeiras chamadas, ok na 3ª (para testar retry)
	mux.HandleFunc("/cart", func(w http.ResponseWriter, r *http.Request) {
		n := fc.call("/cart")
		if n <= 2 {
			slog.Info("  [server] /cart simulando falha transitória", "attempt", n)
			w.WriteHeader(http.StatusServiceUnavailable)
			io.WriteString(w, `{"error":"service temporarily unavailable"}`)
			return
		}
		slog.Info("  [server] /cart ok", "attempt", n)
		io.WriteString(w, `{"items":["notebook","mouse"],"total":549.90}`)
	})

	// /address — sempre ok
	mux.HandleFunc("/address", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"street":"Av. Paulista, 1000","city":"São Paulo","state":"SP"}`)
	})

	// /shipping-calc — flaky: falha na 1ª, ok na 2ª
	mux.HandleFunc("/shipping-calc", func(w http.ResponseWriter, r *http.Request) {
		n := fc.call("/shipping-calc")
		if n == 1 {
			slog.Info("  [server] /shipping-calc simulando falha transitória", "attempt", n)
			w.WriteHeader(http.StatusBadGateway)
			io.WriteString(w, `{"error":"upstream timeout"}`)
			return
		}
		slog.Info("  [server] /shipping-calc ok", "attempt", n)
		io.WriteString(w, `{"carrier":"Correios","price":25.50}`)
	})

	// /tax-calc — sempre ok
	mux.HandleFunc("/tax-calc", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"rate":0.12,"amount":65.99}`)
	})

	// /checkout-confirm — sempre ok
	mux.HandleFunc("/checkout-confirm", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		io.WriteString(w, `{"order_id":"ord_9876","status":"confirmed","eta":"3 dias úteis"}`)
	})

	return httptest.NewServer(mux)
}

// ── Steps compartilhados ──────────────────────────────────────────────────────

func buildCheckoutSteps() []restapi.PipelineStep {
	return []restapi.PipelineStep{
		// ── Wave 0: independentes ─────────────────────────────────────────────
		{
			Name: "token",
			Build: func(ctx context.Context, _ *restapi.Results) (restapi.Request, error) {
				return restapi.Request{Path: "/token"}, nil
			},
		},
		{
			Name: "user-profile",
			Build: func(ctx context.Context, _ *restapi.Results) (restapi.Request, error) {
				return restapi.Request{Path: "/user-profile"}, nil
			},
		},

		// ── Wave 1: dependem de user-profile ─────────────────────────────────
		{
			Name:      "cart",
			DependsOn: []string{"user-profile"},
			// Retry por step: 3 tentativas, backoff exponencial 100ms → 200ms
			Retry: &restapi.RetryPolicy{
				MaxAttempts: 3,
				Delay:       100 * time.Millisecond,
				Backoff:     2.0,
			},
			Build: func(ctx context.Context, prev *restapi.Results) (restapi.Request, error) {
				var u UserProfile
				if err := prev.JSON("user-profile", &u); err != nil {
					return restapi.Request{}, err
				}
				return restapi.Request{
					Path:  "/cart",
					Query: map[string]string{"user_id": u.ID},
				}, nil
			},
		},
		{
			Name:      "address",
			DependsOn: []string{"user-profile"},
			Build: func(ctx context.Context, prev *restapi.Results) (restapi.Request, error) {
				var u UserProfile
				if err := prev.JSON("user-profile", &u); err != nil {
					return restapi.Request{}, err
				}
				return restapi.Request{
					Path:  "/address",
					Query: map[string]string{"user_id": u.ID},
				}, nil
			},
		},

		// ── Wave 2: dependem de cart + address ────────────────────────────────
		{
			Name:      "shipping-calc",
			DependsOn: []string{"cart", "address"},
			// Retry diferente do padrão: fixo, sem backoff
			Retry: &restapi.RetryPolicy{
				MaxAttempts: 2,
				Delay:       50 * time.Millisecond,
				Backoff:     1.0,
			},
			Build: func(ctx context.Context, prev *restapi.Results) (restapi.Request, error) {
				var cart Cart
				var addr Address
				if err := prev.JSON("cart", &cart); err != nil {
					return restapi.Request{}, err
				}
				if err := prev.JSON("address", &addr); err != nil {
					return restapi.Request{}, err
				}
				return restapi.Request{
					Method: "POST",
					Path:   "/shipping-calc",
					Body: map[string]any{
						"destination": addr.City,
						"total":       cart.Total,
					},
				}, nil
			},
		},
		{
			Name:      "tax-calc",
			DependsOn: []string{"cart", "address"},
			Build: func(ctx context.Context, prev *restapi.Results) (restapi.Request, error) {
				var cart Cart
				if err := prev.JSON("cart", &cart); err != nil {
					return restapi.Request{}, err
				}
				return restapi.Request{
					Method: "POST",
					Path:   "/tax-calc",
					Body:   map[string]any{"subtotal": cart.Total},
				}, nil
			},
		},

		// ── Wave 3: depende de shipping-calc + tax-calc ───────────────────────
		{
			Name:      "checkout-confirm",
			DependsOn: []string{"shipping-calc", "tax-calc"},
			Build: func(ctx context.Context, prev *restapi.Results) (restapi.Request, error) {
				var shipping ShippingRate
				var tax TaxResult
				if err := prev.JSON("shipping-calc", &shipping); err != nil {
					return restapi.Request{}, err
				}
				if err := prev.JSON("tax-calc", &tax); err != nil {
					return restapi.Request{}, err
				}
				return restapi.Request{
					Method: "POST",
					Path:   "/checkout-confirm",
					Body: map[string]any{
						"shipping": shipping.Price,
						"tax":      tax.Amount,
					},
				}, nil
			},
		},
	}
}

func main() {
	ctx := context.Background()

	// ──────────────────────────────────────────────────────────────────────────
	// Cenário 1: PIPELINE COM RETRY — user-profile ok, cart e shipping são flaky
	// ──────────────────────────────────────────────────────────────────────────
	fmt.Println()
	slog.Info("════════════════════════════════════════════════════")
	slog.Info("Cenário 1: Retry automático em steps transientes")
	slog.Info("  cart: falha nas tentativas 1 e 2, ok na 3")
	slog.Info("  shipping-calc: falha na tentativa 1, ok na 2")
	slog.Info("════════════════════════════════════════════════════")

	fc1 := newFlakyCounter("/cart", "/shipping-calc")
	server1 := buildTestServer(fc1, false /* user-profile ok */)
	defer server1.Close()

	api1 := restapi.New("checkout-api-1",
		restapi.WithBaseURL(server1.URL),
		restapi.WithTimeout(10*time.Second),
	)
	app1 := core.NewContainer()
	app1.MustRegister(api1)
	if err := app1.InitAll(ctx); err != nil {
		slog.Error("init failed", "err", err)
		os.Exit(1)
	}
	defer app1.ShutdownAll(ctx)

	start := time.Now()
	results1, err1 := api1.Pipeline(ctx, buildCheckoutSteps(),
		// Default retry para steps que não têm Retry declarado
		restapi.WithDefaultRetry(restapi.RetryPolicy{
			MaxAttempts: 2,
			Delay:       50 * time.Millisecond,
			Backoff:     1.5,
		}),
		restapi.WithContinueOnError(),
	)
	elapsed1 := time.Since(start)

	fmt.Println()
	slog.Info("Resultado do pipeline com retry", "wall_time", elapsed1.Round(time.Millisecond))
	printResults(results1)

	if err1 != nil {
		slog.Warn("pipeline com erros parciais", "err", err1)
	}

	// Confirma que checkout foi completado apesar dos retries
	if sr := results1.Get("checkout-confirm"); sr != nil && sr.OK() {
		var out map[string]any
		_ = results1.JSON("checkout-confirm", &out)
		slog.Info("✓ checkout confirmado",
			"order_id", out["order_id"],
			"status", out["status"],
		)
	}

	// ──────────────────────────────────────────────────────────────────────────
	// Cenário 2: CASCADE ABORT — user-profile falha, 4 steps são abortados
	// ──────────────────────────────────────────────────────────────────────────
	fmt.Println()
	slog.Info("════════════════════════════════════════════════════")
	slog.Info("Cenário 2: Cascade abort — user-profile falha")
	slog.Info("  Esperado: cart, address, shipping-calc,")
	slog.Info("  tax-calc e checkout-confirm → todos SKIPPED")
	slog.Info("  (zero chamadas HTTP desnecessárias)")
	slog.Info("════════════════════════════════════════════════════")

	fc2 := newFlakyCounter() // sem falhas transientes — user-profile falha definitivamente
	server2 := buildTestServer(fc2, true /* user-profile retorna 500 */)
	defer server2.Close()

	api2 := restapi.New("checkout-api-2",
		restapi.WithBaseURL(server2.URL),
		restapi.WithTimeout(10*time.Second),
	)
	app2 := core.NewContainer()
	app2.MustRegister(api2)
	if err := app2.InitAll(ctx); err != nil {
		slog.Error("init failed", "err", err)
		os.Exit(1)
	}
	defer app2.ShutdownAll(ctx)

	start = time.Now()
	results2, _ := api2.Pipeline(ctx, buildCheckoutSteps(),
		restapi.WithContinueOnError(), // coleta todos os resultados
	)
	elapsed2 := time.Since(start)

	fmt.Println()
	slog.Info("Resultado do pipeline com cascade abort", "wall_time", elapsed2.Round(time.Millisecond))
	printResults(results2)

	// Análise: identifica a raiz do problema
	fmt.Println()
	skipped := 0
	for _, name := range results2.Names() {
		sr := results2.Get(name)
		if errors.Is(sr.Err, restapi.ErrSkipped) {
			skipped++
		}
	}
	slog.Info("Análise de cascade abort",
		"root_failure", "user-profile",
		"cascade_skipped", skipped,
		"http_calls_saved", skipped, // cada skip evitou pelo menos 1 chamada HTTP
	)

	if sr := results2.Get("user-profile"); sr != nil {
		slog.Error("root cause", "step", "user-profile",
			"http_status", sr.Response.StatusCode,
			"attempts", sr.Attempts,
		)
	}

	// ──────────────────────────────────────────────────────────────────────────
	// Cenário 3: FanOut com retry no default
	// ──────────────────────────────────────────────────────────────────────────
	fmt.Println()
	slog.Info("════════════════════════════════════════════════════")
	slog.Info("Cenário 3: FanOut com DefaultRetry")
	slog.Info("  3 requests paralelos, /cart é flaky (falha 2x)")
	slog.Info("════════════════════════════════════════════════════")

	fc3 := newFlakyCounter("/cart")
	server3 := buildTestServer(fc3, false)
	defer server3.Close()

	api3 := restapi.New("fanout-api",
		restapi.WithBaseURL(server3.URL),
		restapi.WithTimeout(10*time.Second),
	)
	app3 := core.NewContainer()
	app3.MustRegister(api3)
	_ = app3.InitAll(ctx)
	defer app3.ShutdownAll(ctx)

	start = time.Now()
	fanResults, err3 := api3.FanOut(ctx, map[string]restapi.Request{
		"token":        {Path: "/token"},
		"user-profile": {Path: "/user-profile"},
		"cart":         {Path: "/cart", Query: map[string]string{"user_id": "usr_001"}},
	}, restapi.WithDefaultRetry(restapi.RetryPolicy{
		MaxAttempts: 3,
		Delay:       80 * time.Millisecond,
		Backoff:     2.0,
	}))
	elapsed3 := time.Since(start)

	fmt.Println()
	if err3 != nil {
		slog.Error("FanOut falhou", "err", err3)
	} else {
		slog.Info("FanOut com retry concluído", "wall_time", elapsed3.Round(time.Millisecond))
		printResults(fanResults)
		if sr := fanResults.Get("cart"); sr != nil {
			slog.Info("cart resolvido após retries", "attempts", sr.Attempts, "ok", sr.OK())
		}
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func printResults(results *restapi.Results) {
	// Ordem determinística para o relatório
	order := []string{"token", "user-profile", "cart", "address",
		"shipping-calc", "tax-calc", "checkout-confirm", "fanout-api"}

	fmt.Printf("  %-20s  %-10s  %-8s  %s\n", "STEP", "STATUS", "ATTEMPTS", "DETAIL")
	fmt.Printf("  %s\n", repeat("─", 62))

	printed := map[string]bool{}
	for _, name := range order {
		sr := results.Get(name)
		if sr == nil {
			continue
		}
		printed[name] = true
		printRow(sr)
	}
	// Print any steps not in the fixed order
	for _, name := range results.Names() {
		if !printed[name] {
			printRow(results.Get(name))
		}
	}
}

func printRow(sr *restapi.StepResult) {
	status := "✓ ok"
	detail := ""
	if errors.Is(sr.Err, restapi.ErrSkipped) {
		status = "⊘ skipped"
		detail = sr.Err.Error()
	} else if sr.Err != nil {
		status = "✗ error"
		detail = sr.Err.Error()
		if len(detail) > 40 {
			detail = detail[:40] + "…"
		}
	} else if sr.Response != nil {
		detail = fmt.Sprintf("HTTP %d · %s", sr.Response.StatusCode, sr.Latency.Round(time.Millisecond))
	}
	attempts := fmt.Sprintf("%d", sr.Attempts)
	if sr.Attempts == 0 {
		attempts = "-"
	}
	fmt.Printf("  %-20s  %-10s  %-8s  %s\n", sr.Name, status, attempts, detail)
}

func repeat(s string, n int) string {
	result := ""
	for i := 0; i < n; i++ {
		result += s
	}
	return result
}
