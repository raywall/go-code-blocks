// samples/restapi-chained/main.go
//
// Demonstra o padrão de encadeamento de autenticação entre blocos REST API,
// integrado com DynamoDB e o bloco de decisão.
//
// Cenário: gateway de pagamentos
//
//  1. 'auth-block'     → obtém um Bearer token via OAuth2 client_credentials
//     e implementa TokenProvider
//  2. 'payments-api'  → usa auth-block como TokenProvider; cada request
//     envia automaticamente o token correto
//  3. 'fraud-engine'  → avalia as regras CEL para decidir se o pagamento
//     passa por validação adicional ou é aprovado diretamente
//  4. 'transactions'  → registra o resultado no DynamoDB
//
// A chave do padrão: nenhum bloco conhece o segredo dos outros. O Container
// compõe os blocos e o token flui pelo WithTokenProvider sem acoplamento.
//
// Variáveis de ambiente:
//
//	AUTH_TOKEN_URL      endpoint de token OAuth2
//	AUTH_CLIENT_ID      client_id da aplicação
//	AUTH_CLIENT_SECRET  client_secret da aplicação
//	PAYMENTS_API_URL    base URL da API de pagamentos
//	AWS_REGION          região AWS (default: us-east-1)
//	TRANSACTIONS_TABLE  tabela DynamoDB (default: transactions-dev)
//	REDIS_ADDR          Redis (default: localhost:6379)
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/raywall/go-code-blocks/blocks/decision"
	"github.com/raywall/go-code-blocks/blocks/dynamodb"
	"github.com/raywall/go-code-blocks/blocks/redis"
	"github.com/raywall/go-code-blocks/blocks/restapi"
	"github.com/raywall/go-code-blocks/core"
)

// ── Modelos ───────────────────────────────────────────────────────────────────

// PaymentRequest é o payload recebido pelo nosso serviço.
type PaymentRequest struct {
	ID         string  `json:"id"`
	Amount     float64 `json:"amount"   decision:"amount"`
	Currency   string  `json:"currency" decision:"currency"`
	Method     string  `json:"method"   decision:"method"` // "credit_card" | "pix" | "boleto"
	MerchantID string  `json:"merchant_id" decision:"-"`
}

// ExternalPaymentPayload é o que enviamos para a API externa.
type ExternalPaymentPayload struct {
	ReferenceID string  `json:"reference_id"`
	Amount      float64 `json:"amount"`
	Currency    string  `json:"currency"`
	Method      string  `json:"payment_method"`
}

// ExternalPaymentResponse é o que a API externa retorna.
type ExternalPaymentResponse struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Code   string `json:"code"`
}

// Transaction é o registro persistido no DynamoDB.
type Transaction struct {
	ID             string  `dynamodbav:"id"`
	PaymentID      string  `dynamodbav:"payment_id"`
	ExternalID     string  `dynamodbav:"external_id"`
	Amount         float64 `dynamodbav:"amount"`
	Currency       string  `dynamodbav:"currency"`
	Status         string  `dynamodbav:"status"`
	RequiredReview bool    `dynamodbav:"required_review"`
	CreatedAt      string  `dynamodbav:"created_at"`
}

// ── Nomes dos blocos ──────────────────────────────────────────────────────────

const (
	blockAuth         = "auth"
	blockPaymentsAPI  = "payments-api"
	blockFraudEngine  = "fraud-engine"
	blockTransactions = "transactions"
	blockCache        = "token-cache"
)

// ── Nomes das regras ──────────────────────────────────────────────────────────

const (
	ruleHighValue    = "high-value"     // amount > 10000 → revisão
	ruleInstantPix   = "instant-pix"    // pix com qualquer valor → aprovação instantânea
	ruleCardLowValue = "card-low-value" // cartão <= 500 → aprovação direta
)

func main() {
	ctx := context.Background()

	// ── Bloco de autenticação — OAuth2 client_credentials ─────────────────────
	// Este bloco obtém e renova o Bearer token automaticamente.
	// Ele implementa TokenProvider e pode ser injetado em outros blocos.
	authBlock := restapi.New(blockAuth,
		restapi.WithBaseURL(envOr("AUTH_TOKEN_URL", "https://auth.example.com")),
		restapi.WithTimeout(5*time.Second),
		restapi.WithOAuth2ClientCredentials(
			envOr("AUTH_TOKEN_URL", "https://auth.example.com/oauth/token"),
			envOr("AUTH_CLIENT_ID", "my-client-id"),
			envOr("AUTH_CLIENT_SECRET", "my-client-secret"),
			"payments:write", "payments:read",
		),
	)

	// ── Bloco da API de pagamentos — usa authBlock como provider de token ──────
	// O token é buscado/renovado automaticamente pelo authBlock antes de
	// cada chamada. payments-api não conhece CLIENT_ID nem CLIENT_SECRET.
	paymentsAPI := restapi.New(blockPaymentsAPI,
		restapi.WithBaseURL(envOr("PAYMENTS_API_URL", "https://payments.example.com/v1")),
		restapi.WithTimeout(15*time.Second),
		restapi.WithTokenProvider(authBlock), // ← encadeamento de token
		restapi.WithHeader("X-Idempotency-Key", "auto"),
		restapi.WithHeader("X-Service", "go-code-blocks"),
	)

	// ── Bloco de decisão — regras de roteamento de pagamento ──────────────────
	fraudEngine := decision.New(blockFraudEngine,
		decision.WithRule(
			ruleHighValue,
			`amount > 10000.0`,
			decision.Schema{"amount": decision.Float},
		),
		decision.WithRule(
			ruleInstantPix,
			`method == "pix"`,
			decision.Schema{"method": decision.String},
		),
		decision.WithRule(
			ruleCardLowValue,
			`method == "credit_card" && amount <= 500.0`,
			decision.Schema{
				"method": decision.String,
				"amount": decision.Float,
			},
		),
	)

	// ── Bloco DynamoDB — persistência de transações ────────────────────────────
	transactions := dynamodb.New[Transaction](blockTransactions,
		dynamodb.WithRegion(envOr("AWS_REGION", "us-east-1")),
		dynamodb.WithTable(envOr("TRANSACTIONS_TABLE", "transactions-dev")),
		dynamodb.WithPartitionKey("id"),
	)

	// ── Bloco Redis — cache do token (opcional, para inspeção) ─────────────────
	tokenCache := redis.New(blockCache,
		redis.WithAddr(envOr("REDIS_ADDR", "localhost:6379")),
		redis.WithKeyPrefix("tokens:"),
		redis.WithDialTimeout(2*time.Second),
	)

	// ── Container ─────────────────────────────────────────────────────────────
	// A ordem importa: authBlock deve ser registrado antes de paymentsAPI
	// para que Init resolva dependências na ordem correta.
	app := core.NewContainer()
	app.MustRegister(authBlock)
	app.MustRegister(paymentsAPI)
	app.MustRegister(fraudEngine)
	app.MustRegister(transactions)
	app.MustRegister(tokenCache)

	if err := app.InitAll(ctx); err != nil {
		slog.Error("init failed", "err", err)
		os.Exit(1)
	}
	defer app.ShutdownAll(ctx)

	slog.Info("all blocks ready",
		"auth", blockAuth,
		"api", blockPaymentsAPI,
		"rules", []string{ruleHighValue, ruleInstantPix, ruleCardLowValue},
	)

	// ── Simular pagamentos variados ───────────────────────────────────────────
	payments := []PaymentRequest{
		{ID: "pay_001", Amount: 250.00, Currency: "BRL", Method: "credit_card", MerchantID: "mrch_1"},
		{ID: "pay_002", Amount: 15000.00, Currency: "BRL", Method: "credit_card", MerchantID: "mrch_2"},
		{ID: "pay_003", Amount: 890.00, Currency: "BRL", Method: "pix", MerchantID: "mrch_1"},
		{ID: "pay_004", Amount: 50000.00, Currency: "BRL", Method: "pix", MerchantID: "mrch_3"},
		{ID: "pay_005", Amount: 300.00, Currency: "USD", Method: "boleto", MerchantID: "mrch_1"},
	}

	for _, p := range payments {
		fmt.Println()
		if err := processPayment(ctx, fraudEngine, paymentsAPI, transactions, tokenCache, p); err != nil {
			slog.Error("payment failed", "id", p.ID, "err", err)
		}
	}
}

// processPayment orquestra o fluxo completo de um pagamento.
func processPayment(
	ctx context.Context,
	engine *decision.Block,
	api *restapi.Block,
	transactions *dynamodb.Block[Transaction],
	cache *redis.Block,
	p PaymentRequest,
) error {
	slog.Info("processing payment",
		"id", p.ID, "method", p.Method,
		"amount", p.Amount, "currency", p.Currency,
	)

	// ── 1. Avaliar regras CEL usando struct tags ───────────────────────────────
	result, err := engine.EvaluateAllFrom(ctx, p)
	if err != nil {
		return fmt.Errorf("fraud engine: %w", err)
	}
	slog.Info("rules evaluated",
		"passed", result.PassedNames(),
		"any", result.Any(),
	)

	// ── 2. Decidir fluxo com base nas regras ──────────────────────────────────
	requiresReview := result.Passed(ruleHighValue)

	switch {
	case result.Passed(ruleHighValue) && result.Passed(ruleInstantPix):
		// PIX de alto valor: aprovação automática mas com flag de revisão
		slog.Info("▶ high-value PIX: instant approval + compliance flag",
			"amount", p.Amount)

	case result.Passed(ruleHighValue):
		// Alto valor não-PIX: enfileirar para revisão manual
		slog.Info("▶ high-value: queued for manual review", "amount", p.Amount)

	case result.Passed(ruleInstantPix):
		// PIX normal: aprovação instantânea
		slog.Info("▶ PIX instant approval", "amount", p.Amount)

	case result.Passed(ruleCardLowValue):
		// Cartão de baixo valor: aprovação direta
		slog.Info("▶ card low-value: direct approval", "amount", p.Amount)

	default:
		// Boleto ou regra não mapeada → fluxo padrão
		slog.Info("▶ standard flow", "method", p.Method, "amount", p.Amount)
	}

	// ── 3. Chamar API de pagamentos externa ───────────────────────────────────
	// O token OAuth2 é obtido/renovado pelo authBlock transparentemente.
	payload := ExternalPaymentPayload{
		ReferenceID: p.ID,
		Amount:      p.Amount,
		Currency:    p.Currency,
		Method:      p.Method,
	}

	var extResp ExternalPaymentResponse
	err = api.PostJSON(ctx, "/payments", payload, &extResp)
	if err != nil {
		// Em ambiente de demo, a API externa não existe — continuamos mesmo assim.
		slog.Warn("external API unavailable (expected in demo env)", "err", err)
		extResp = ExternalPaymentResponse{
			ID:     "ext_" + p.ID,
			Status: "simulated",
			Code:   "SIM_200",
		}
	} else {
		slog.Info("external payment response",
			"external_id", extResp.ID,
			"status", extResp.Status,
		)
	}

	// ── 4. Persistir transação no DynamoDB ────────────────────────────────────
	tx := Transaction{
		ID:             "tx_" + p.ID,
		PaymentID:      p.ID,
		ExternalID:     extResp.ID,
		Amount:         p.Amount,
		Currency:       p.Currency,
		Status:         extResp.Status,
		RequiredReview: requiresReview,
		CreatedAt:      time.Now().UTC().Format(time.RFC3339),
	}

	if err := transactions.PutItem(ctx, tx); err != nil {
		slog.Warn("persist transaction failed (expected in demo env)", "err", err)
	} else {
		slog.Info("✓ transaction persisted",
			"tx_id", tx.ID,
			"status", tx.Status,
			"required_review", tx.RequiredReview,
		)
	}

	// ── 5. Cachear token atual para inspeção ──────────────────────────────────
	// Em produção isso seria monitoramento/observabilidade, não armazenamento real.
	_ = cache.Set(ctx, "last_tx_id", tx.ID, 5*time.Minute)

	return nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
