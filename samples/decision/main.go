// samples/decision/main.go
//
// Demonstra como usar o bloco de decisão (CEL) para rotear fluxos de negócio
// de forma declarativa, combinando-o com blocos de integração como DynamoDB.
//
// Cenário implementado:
//
//	Uma API de cadastro recebe requests de clientes.
//	A regra de negócio é:
//	  • Cliente PJ com receita >= 1000  → busca dados no DynamoDB (atendimento full)
//	  • Cliente PJ com receita < 1000   → busca dados no DynamoDB (atendimento básico)
//	  • Cliente PF                      → rejeita com erro de negócio
//	  • Cliente com tipo desconhecido   → rejeita com erro de validação
//
// O bloco de decisão avalia as regras CEL sem nenhum if/else espalhado pelo
// código da aplicação. O resultado é um *decision.Result que pode ser consultado
// por nome de regra de forma fluente.
//
// Variáveis de ambiente:
//
//	AWS_REGION    região AWS        (default: us-east-1)
//	CLIENTS_TABLE tabela DynamoDB   (default: clients-dev)
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/raywall/go-code-blocks/blocks/decision"
	"github.com/raywall/go-code-blocks/blocks/dynamodb"
	"github.com/raywall/go-code-blocks/core"
)

// ── Modelos ───────────────────────────────────────────────────────────────────

// CustomerRequest é o payload de entrada da API.
type CustomerRequest struct {
	ID           string  `json:"id"`
	CustomerType string  `json:"customer_type"` // "PJ" | "PF"
	Revenue      float64 `json:"revenue"`
	Name         string  `json:"name"`
}

// ClientRecord é o modelo armazenado no DynamoDB.
type ClientRecord struct {
	ID           string  `dynamodbav:"id"            json:"id"`
	CustomerType string  `dynamodbav:"customer_type" json:"customer_type"`
	Name         string  `dynamodbav:"name"          json:"name"`
	Revenue      float64 `dynamodbav:"revenue"       json:"revenue"`
	Plan         string  `dynamodbav:"plan"          json:"plan"`
	RegisteredAt string  `dynamodbav:"registered_at" json:"registered_at"`
}

// ── Erros de negócio ──────────────────────────────────────────────────────────

var (
	ErrPersonaFisicaNotSupported = errors.New("pessoa física não é atendida neste canal")
	ErrUnknownCustomerType       = errors.New("tipo de cliente desconhecido")
)

// ── Nomes dos blocos ──────────────────────────────────────────────────────────

const (
	blockRouter  = "customer-router"
	blockClients = "clients"
)

// ── Nomes das regras CEL ──────────────────────────────────────────────────────

const (
	rulePJHighValue = "pj-high-value" // PJ com receita >= 1000
	rulePJLowValue  = "pj-low-value"  // PJ com receita < 1000
	rulePF          = "is-pf"         // qualquer PF
)

func main() {
	ctx := context.Background()

	// ── Bloco de decisão — regras CEL compiladas no Init ─────────────────────
	//
	// O schema declara os tipos das variáveis usadas nas expressões.
	// A validação de tipos ocorre antes da avaliação, impedindo erros silenciosos.
	pjSchema := decision.Schema{
		"customer_type": decision.String,
		"revenue":       decision.Float,
	}

	router := decision.New(blockRouter,
		decision.WithRule(
			rulePJHighValue,
			`customer_type == "PJ" && revenue >= 1000.0`,
			pjSchema,
		),
		decision.WithRule(
			rulePJLowValue,
			`customer_type == "PJ" && revenue < 1000.0`,
			pjSchema,
		),
		decision.WithRule(
			rulePF,
			`customer_type == "PF"`,
			decision.Schema{"customer_type": decision.String},
		),
	)

	// ── Bloco DynamoDB ────────────────────────────────────────────────────────
	clients := dynamodb.New[ClientRecord](blockClients,
		dynamodb.WithRegion(envOr("AWS_REGION", "us-east-1")),
		dynamodb.WithTable(envOr("CLIENTS_TABLE", "clients-dev")),
		dynamodb.WithPartitionKey("id"),
	)

	// ── Container ─────────────────────────────────────────────────────────────
	app := core.NewContainer()
	app.MustRegister(router)
	app.MustRegister(clients)

	if err := app.InitAll(ctx); err != nil {
		slog.Error("init failed", "err", err)
		os.Exit(1)
	}
	defer app.ShutdownAll(ctx)

	slog.Info("blocks ready",
		"rules", []string{rulePJHighValue, rulePJLowValue, rulePF},
	)

	// ── Seed: inserir clientes de teste ───────────────────────────────────────
	now := time.Now().UTC().Format(time.RFC3339)
	seeds := []ClientRecord{
		{ID: "cli_001", CustomerType: "PJ", Name: "Acme Corp", Revenue: 15000, Plan: "enterprise", RegisteredAt: now},
		{ID: "cli_002", CustomerType: "PJ", Name: "Startup Ltda", Revenue: 500, Plan: "basic", RegisteredAt: now},
		{ID: "cli_003", CustomerType: "PF", Name: "João Silva", Revenue: 3000, Plan: "", RegisteredAt: now},
	}
	for _, c := range seeds {
		if err := clients.PutItem(ctx, c); err != nil {
			slog.Warn("seed failed (non-fatal in demo env)", "id", c.ID, "err", err)
		}
	}

	// ── Simular requests ──────────────────────────────────────────────────────
	requests := []CustomerRequest{
		{ID: "cli_001", CustomerType: "PJ", Revenue: 15000, Name: "Acme Corp"},
		{ID: "cli_002", CustomerType: "PJ", Revenue: 500, Name: "Startup Ltda"},
		{ID: "cli_003", CustomerType: "PF", Revenue: 3000, Name: "João Silva"},
		{ID: "cli_999", CustomerType: "GOV", Revenue: 0, Name: "Unknown Org"},
	}

	for _, req := range requests {
		fmt.Println()
		slog.Info("processing request",
			"id", req.ID,
			"type", req.CustomerType,
			"revenue", req.Revenue,
		)

		if err := handleRequest(ctx, router, clients, req); err != nil {
			slog.Error("request rejected", "id", req.ID, "reason", err)
		}
	}
}

// handleRequest encapsula a lógica de roteamento baseada em regras CEL.
// A função nunca usa if/else para verificar tipos de cliente diretamente —
// toda a lógica de decisão é delegada ao bloco de decisão.
func handleRequest(
	ctx context.Context,
	router *decision.Block,
	clients *dynamodb.Block[ClientRecord],
	req CustomerRequest,
) error {
	// Monta o mapa de variáveis de input para as regras CEL.
	// Apenas os campos relevantes são incluídos — o bloco de decisão
	// valida que as variáveis certas estão presentes para cada regra.
	inputs := map[string]any{
		"customer_type": req.CustomerType,
		"revenue":       req.Revenue,
	}

	// Avalia todas as regras concorrentemente.
	result, err := router.EvaluateAll(ctx, inputs)
	if err != nil {
		return fmt.Errorf("rule evaluation failed: %w", err)
	}

	slog.Info("rule results",
		"passed", result.PassedNames(),
		"failed", result.FailedNames(),
	)

	// ── Roteamento baseado no resultado ───────────────────────────────────────

	switch {
	case result.Passed(rulePF):
		// PF detectado — rejeita imediatamente com erro de negócio.
		return ErrPersonaFisicaNotSupported

	case result.Passed(rulePJHighValue):
		// PJ high-value → atendimento completo com dados do DynamoDB.
		return handlePJHighValue(ctx, clients, req.ID)

	case result.Passed(rulePJLowValue):
		// PJ low-value → atendimento básico com dados do DynamoDB.
		return handlePJLowValue(ctx, clients, req.ID)

	default:
		// Nenhuma regra passou → tipo desconhecido.
		return fmt.Errorf("%w: %q", ErrUnknownCustomerType, req.CustomerType)
	}
}

func handlePJHighValue(ctx context.Context, clients *dynamodb.Block[ClientRecord], id string) error {
	slog.Info("▶ route: PJ high-value — full service", "id", id)

	client, err := clients.GetItem(ctx, id, nil)
	if err != nil {
		if errors.Is(err, core.ErrItemNotFound) {
			return fmt.Errorf("client %q not found in database", id)
		}
		return fmt.Errorf("database error: %w", err)
	}

	slog.Info("✓ client data retrieved",
		"id", client.ID,
		"name", client.Name,
		"plan", client.Plan,
		"revenue", client.Revenue,
	)
	slog.Info("  → applying enterprise SLA, dedicated support, full API access")
	return nil
}

func handlePJLowValue(ctx context.Context, clients *dynamodb.Block[ClientRecord], id string) error {
	slog.Info("▶ route: PJ low-value — basic service", "id", id)

	client, err := clients.GetItem(ctx, id, nil)
	if err != nil {
		if errors.Is(err, core.ErrItemNotFound) {
			return fmt.Errorf("client %q not found in database", id)
		}
		return fmt.Errorf("database error: %w", err)
	}

	slog.Info("✓ client data retrieved",
		"id", client.ID,
		"name", client.Name,
		"plan", client.Plan,
		"revenue", client.Revenue,
	)
	slog.Info("  → applying standard SLA, self-service support, limited API access")
	return nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
