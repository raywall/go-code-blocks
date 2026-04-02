// samples/flow-order/main.go
//
// Demonstra o ciclo completo de um nano serviço usando os pacotes
// flow, transform e output do go-code-blocks:
//
//	Entrada      → server.HTTPBlock          POST /orders
//	Validação    → flow.Validate             CEL: customer_type == "PJ"
//	Enrich       → flow.EnrichStep           busca cliente no DynamoDB
//	Enrich       → flow.EnrichStep           busca crédito (output.CallJSON)
//	Decisão      → flow.DecideStep           CEL: amount <= credit_available
//	Gate         → flow.AbortIf              aborta com 402 se reprovado
//	Transformação→ flow.NewStep+transform    monta payload do pedido
//	Saída        → flow.NewStep+output.Created  responde HTTP 201
//
// Variáveis de ambiente:
//
//	PORT             porta HTTP              (default: 8080)
//	AWS_REGION       região AWS              (default: us-east-1)
//	CUSTOMERS_TABLE  tabela DynamoDB         (default: customers-dev)
//	DYNAMO_ENDPOINT  endpoint local          (default: http://localhost:8000)
//	CREDIT_API_URL   URL da API de crédito   (default: https://api.example.com)
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
	"github.com/raywall/go-code-blocks/blocks/flow"
	"github.com/raywall/go-code-blocks/blocks/output"
	"github.com/raywall/go-code-blocks/blocks/restapi"
	"github.com/raywall/go-code-blocks/blocks/server"
	"github.com/raywall/go-code-blocks/blocks/transform"
	"github.com/raywall/go-code-blocks/core"
)

// ── Modelos ───────────────────────────────────────────────────────────────────

type Customer struct {
	ID    string `dynamodbav:"id"            json:"id"            decision:"-"`
	Name  string `dynamodbav:"name"          json:"name"          decision:"-"`
	Type  string `dynamodbav:"customer_type" json:"customer_type" decision:"customer_type"`
	TaxID string `dynamodbav:"tax_id"        json:"tax_id"        decision:"-"`
}

type OrderRequest struct {
	CustomerID string  `json:"customer_id"`
	Amount     float64 `json:"amount"`
	Items      []any   `json:"items"`
}

type CreditResponse struct {
	Limit     float64 `json:"limit"`
	Available float64 `json:"available"`
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// ── Blocos de integração ──────────────────────────────────────────────────

	customersDB := dynamodb.New[Customer]("customers",
		dynamodb.WithRegion(envOr("AWS_REGION", "us-east-1")),
		dynamodb.WithTable(envOr("CUSTOMERS_TABLE", "customers-dev")),
		dynamodb.WithPartitionKey("id"),
		dynamodb.WithEndpoint(envOr("DYNAMO_ENDPOINT", "http://localhost:8000")),
	)

	creditAPI := restapi.New("credit-api",
		restapi.WithBaseURL(envOr("CREDIT_API_URL", "https://api.example.com")),
		restapi.WithTimeout(5*time.Second),
	)

	rules := decision.New("rules",
		decision.WithRule("is-pj",
			`customer_type == "PJ"`,
			decision.Schema{"customer_type": decision.String},
		),
		decision.WithRule("limit-ok",
			`amount <= credit_available`,
			decision.Schema{
				"amount":           decision.Float,
				"credit_available": decision.Float,
			},
		),
	)

	// ── Flow ──────────────────────────────────────────────────────────────────

	orderFlow := flow.New("create-order",

		// 1. Validação — header X-Customer-Type deve ser "PJ"
		flow.NewStep("validate-type",
			flow.Validate(rules, "is-pj",
				func(req *server.Request, _ *flow.State) map[string]any {
					return map[string]any{"customer_type": req.Header("X-Customer-Type")}
				},
				flow.WithFailStatus(http.StatusForbidden),
				flow.WithFailMessage(func(_ string) string {
					return "apenas clientes PJ são atendidos neste canal"
				}),
			)),

		// 2. Enrich — busca cliente no DynamoDB
		flow.EnrichStep("load-customer",
			func(ctx context.Context, req *server.Request, _ *flow.State) (any, error) {
				var body OrderRequest
				if err := req.BindJSON(&body); err != nil {
					return nil, fmt.Errorf("body inválido: %w", err)
				}
				return customersDB.GetItem(ctx, body.CustomerID, nil)
			}),

		// 3. Enrich — busca limite de crédito via REST API usando output.CallJSON
		// output.REST constrói o restapi.Request a partir do estado atual,
		// resolvendo o {tax_id} do cliente carregado no step anterior.
		flow.EnrichStep("load-credit",
			output.CallJSON(creditAPI, &CreditResponse{},
				output.REST("GET", "/credit/{tax_id}").
					PathParamFromState("tax_id", "load-customer", "tax_id").
					HeaderFromRequest("X-Trace-Id"),
			)),

		// 4. Decisão — avalia se amount <= credit_available
		flow.DecideStep("check-limit", rules,
			func(req *server.Request, s *flow.State) map[string]any {
				var body OrderRequest
				req.BindJSON(&body)
				var credit CreditResponse
				s.Bind("load-credit", &credit)
				return map[string]any{
					"amount":           body.Amount,
					"credit_available": credit.Available,
				}
			}),

		// 5. Gate — aborta com 402 se limite insuficiente
		flow.NewStep("gate-credit",
			flow.AbortIf(
				func(s *flow.State) bool { return s.Failed("check-limit", "limit-ok") },
				func(s *flow.State) *server.Response {
					var credit CreditResponse
					s.Bind("load-credit", &credit)
					return server.JSON(http.StatusPaymentRequired, map[string]any{
						"error":            "limite de crédito insuficiente",
						"credit_available": credit.Available,
					})
				},
			)),

		// 6. Transform — monta o payload do pedido usando transform.Builder
		// Combina campos de múltiplas fontes de forma declarativa.
		flow.NewStep("build-order",
			flow.Transform(func(ctx context.Context, req *server.Request, s *flow.State) error {
				var body OrderRequest
				req.BindJSON(&body)

				var credit CreditResponse
				s.Bind("load-credit", &credit)

				payload, err := transform.New(s, req).
					// campos do cliente (apenas os relevantes para o pedido)
					Pick("load-customer", "id", "name", "tax_id").
					// campo calculado: saldo após o pedido
					Field("credit_after", "load-credit", "available").
					// campos dinâmicos
					Compute("credit_after", func(s *flow.State, _ *server.Request) any {
						return credit.Available - body.Amount
					}).
					// campos estáticos e da requisição
					Set("amount", body.Amount).
					Set("items", body.Items).
					Set("status", "pending").
					Set("order_id", fmt.Sprintf("ord_%d", time.Now().UnixMilli())).
					Set("created_at", time.Now().UTC().Format(time.RFC3339)).
					Build()

				if err != nil {
					return err
				}
				s.Set("order", payload)
				return nil
			})),

		// 7. Respond — HTTP 201 com o pedido criado
		// output.Created lê state["order"] e serializa como JSON 201.
		flow.NewStep("respond", output.Created("order")),
	)

	// ── Router ────────────────────────────────────────────────────────────────

	router := server.NewRouter()

	router.GET("/health", func(ctx context.Context, req *server.Request) (*server.Response, error) {
		return server.JSON(http.StatusOK, map[string]string{
			"status":    "ok",
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		}), nil
	})

	router.POST("/orders", orderFlow.Handler())

	// ── Servidor ──────────────────────────────────────────────────────────────

	port := envOrInt("PORT", 8080)

	apiBlock := server.NewHTTP("api",
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
	app.MustRegister(customersDB)
	app.MustRegister(creditAPI)
	app.MustRegister(rules)
	app.MustRegister(orderFlow)
	app.MustRegister(apiBlock)

	if err := app.InitAll(ctx); err != nil {
		slog.Error("init failed", "err", err)
		os.Exit(1)
	}

	slog.Info("servidor pronto", "port", port)
	<-ctx.Done()

	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = app.ShutdownAll(shutCtx)
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
