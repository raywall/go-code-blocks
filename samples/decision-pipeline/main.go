// samples/decision-pipeline/main.go
//
// Demonstra um pipeline completo onde o bloco de decisão usa struct tags
// (`decision:`) para extrair variáveis diretamente de structs de domínio,
// combinando DynamoDB, Redis e Parameter Store num fluxo guiado por regras CEL.
//
// Cenário: processamento de contratos
//
//  1. Carrega configuração de negócio do SSM (limites, flags)
//  2. Tenta cache no Redis antes de ir ao DynamoDB
//  3. Aplica regras CEL sobre o contrato para decidir o fluxo:
//     • "eligible-for-credit"  → produto PJ + valor <= limite de crédito do SSM
//     • "needs-compliance"     → valor > 50000 (passa por análise adicional)
//     • "auto-approve"         → valor <= 5000 (aprovação automática)
//  4. Registra resultado no DynamoDB e invalida cache
//
// Variáveis de ambiente:
//
//	AWS_REGION       região AWS           (default: us-east-1)
//	CONTRACTS_TABLE  tabela de contratos  (default: contracts-dev)
//	REDIS_ADDR       endereço Redis       (default: localhost:6379)
//	SSM_PATH_PREFIX  prefixo SSM          (default: /myapp/dev)
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/raywall/go-code-blocks/blocks/decision"
	"github.com/raywall/go-code-blocks/blocks/dynamodb"
	"github.com/raywall/go-code-blocks/blocks/parameterstore"
	"github.com/raywall/go-code-blocks/blocks/redis"
	"github.com/raywall/go-code-blocks/core"
)

// ── Modelos ───────────────────────────────────────────────────────────────────

// Contract é o modelo de domínio com tags `decision:` para extração automática
// e `dynamodbav:` para persistência.
type Contract struct {
	ID        string  `dynamodbav:"id"         json:"id"         decision:"-"`
	Product   string  `dynamodbav:"product"    json:"product"    decision:"product"`
	Value     float64 `dynamodbav:"value"      json:"value"      decision:"value"`
	Status    string  `dynamodbav:"status"     json:"status"     decision:"status"`
	ClientID  string  `dynamodbav:"client_id"  json:"client_id"  decision:"-"`
	CreatedAt string  `dynamodbav:"created_at" json:"created_at" decision:"-"`
	UpdatedAt string  `dynamodbav:"updated_at" json:"updated_at" decision:"-"`
}

// AppLimits é carregado do Parameter Store durante o boot.
type AppLimits struct {
	MaxCreditValue float64
	AutoApproveMax float64
	ComplianceMin  float64
}

// ── Nomes dos blocos ──────────────────────────────────────────────────────────

const (
	blockDecision  = "contract-rules"
	blockContracts = "contracts"
	blockCache     = "cache"
	blockConfig    = "config"
)

// ── Nomes das regras ──────────────────────────────────────────────────────────

const (
	ruleEligibleForCredit = "eligible-for-credit"
	ruleNeedsCompliance   = "needs-compliance"
	ruleAutoApprove       = "auto-approve"
	rulePendingOnly       = "pending-only"
)

func main() {
	ctx := context.Background()

	// ── Schema compartilhado pelas regras de contrato ─────────────────────────
	contractSchema := decision.Schema{
		"product": decision.String,
		"value":   decision.Float,
		"status":  decision.String,
	}

	// ── Bloco de decisão ──────────────────────────────────────────────────────
	engine := decision.New(blockDecision,
		// Elegível para crédito: produto PJ e valor dentro do limite
		decision.WithRule(
			ruleEligibleForCredit,
			`product == "PJ" && value <= 100000.0`,
			contractSchema,
		),
		// Precisa de compliance: valor alto, independente do produto
		decision.WithRule(
			ruleNeedsCompliance,
			`value > 50000.0`,
			contractSchema,
		),
		// Aprovação automática: valor pequeno e produto PJ
		decision.WithRule(
			ruleAutoApprove,
			`product == "PJ" && value <= 5000.0`,
			contractSchema,
		),
		// Apenas contratos pendentes podem ser processados
		decision.WithRule(
			rulePendingOnly,
			`status == "pending"`,
			decision.Schema{"status": decision.String},
		),
	)

	// ── Demais blocos ─────────────────────────────────────────────────────────
	contracts := dynamodb.New[Contract](blockContracts,
		dynamodb.WithRegion(envOr("AWS_REGION", "us-east-1")),
		dynamodb.WithTable(envOr("CONTRACTS_TABLE", "contracts-dev")),
		dynamodb.WithPartitionKey("id"),
	)

	cache := redis.New(blockCache,
		redis.WithAddr(envOr("REDIS_ADDR", "localhost:6379")),
		redis.WithKeyPrefix("contracts:"),
		redis.WithDialTimeout(2*time.Second),
	)

	cfg := parameterstore.New(blockConfig,
		parameterstore.WithRegion(envOr("AWS_REGION", "us-east-1")),
		parameterstore.WithPathPrefix(envOr("SSM_PATH_PREFIX", "/myapp/dev")),
		parameterstore.WithDecryption(),
	)

	app := core.NewContainer()
	app.MustRegister(engine)
	app.MustRegister(contracts)
	app.MustRegister(cache)
	app.MustRegister(cfg)

	if err := app.InitAll(ctx); err != nil {
		slog.Error("init failed", "err", err)
		os.Exit(1)
	}
	defer app.ShutdownAll(ctx)

	// ── 1. Carregar limites de negócio do SSM ─────────────────────────────────
	slog.Info("═══ Boot: carregando configuração ═══")
	limits := loadLimits(ctx, cfg)
	slog.Info("limits loaded",
		"max_credit", limits.MaxCreditValue,
		"auto_approve_max", limits.AutoApproveMax,
		"compliance_min", limits.ComplianceMin,
	)

	// ── 2. Seed de contratos de teste ─────────────────────────────────────────
	now := time.Now().UTC().Format(time.RFC3339)
	testContracts := []Contract{
		{ID: "ctr_001", Product: "PJ", Value: 3500, Status: "pending", ClientID: "cli_001", CreatedAt: now, UpdatedAt: now},
		{ID: "ctr_002", Product: "PJ", Value: 75000, Status: "pending", ClientID: "cli_002", CreatedAt: now, UpdatedAt: now},
		{ID: "ctr_003", Product: "PF", Value: 1200, Status: "pending", ClientID: "cli_003", CreatedAt: now, UpdatedAt: now},
		{ID: "ctr_004", Product: "PJ", Value: 150000, Status: "pending", ClientID: "cli_004", CreatedAt: now, UpdatedAt: now},
		{ID: "ctr_005", Product: "PJ", Value: 2000, Status: "approved", ClientID: "cli_005", CreatedAt: now, UpdatedAt: now},
	}
	for _, c := range testContracts {
		if err := contracts.PutItem(ctx, c); err != nil {
			slog.Warn("seed failed", "id", c.ID, "err", err)
		}
	}

	// ── 3. Processar cada contrato ────────────────────────────────────────────
	slog.Info("═══ Processando contratos ═══")
	for _, c := range testContracts {
		fmt.Println()
		if err := processContract(ctx, engine, contracts, cache, c, limits); err != nil {
			slog.Error("contract rejected",
				"id", c.ID, "product", c.Product, "value", c.Value,
				"reason", err,
			)
		}
	}
}

// processContract orquestra o fluxo de um contrato usando regras CEL.
func processContract(
	ctx context.Context,
	engine *decision.Block,
	contracts *dynamodb.Block[Contract],
	cache *redis.Block,
	contract Contract,
	limits AppLimits,
) error {
	slog.Info("processing",
		"id", contract.ID, "product", contract.Product,
		"value", contract.Value, "status", contract.Status,
	)

	// ── Verifica cache antes do banco ─────────────────────────────────────────
	cacheKey := "contract:" + contract.ID
	var cached Contract
	if err := cache.GetJSON(ctx, cacheKey, &cached); err == nil {
		slog.Info("cache HIT", "id", cached.ID)
		contract = cached
	} else {
		// Busca no DynamoDB e popula o cache
		if fetched, err := contracts.GetItem(ctx, contract.ID, nil); err == nil {
			contract = fetched
			_ = cache.SetJSON(ctx, cacheKey, contract, 10*time.Minute)
			slog.Info("cache MISS — loaded from DynamoDB", "id", contract.ID)
		}
	}

	// ── EvaluateFrom: extrai variáveis direto dos campos com tag `decision:` ──
	// Não é necessário construir um map manualmente — a engine lê as tags.
	result, err := engine.EvaluateAllFrom(ctx, contract)
	if err != nil {
		return fmt.Errorf("rule evaluation: %w", err)
	}

	slog.Info("rules evaluated",
		"passed", result.PassedNames(),
		"failed", result.FailedNames(),
	)

	// ── Guarda de status: só contratos pendentes são processados ──────────────
	if result.Failed(rulePendingOnly) {
		return fmt.Errorf("contract %q already processed (status=%q)", contract.ID, contract.Status)
	}

	// ── Roteamento por resultado das regras ───────────────────────────────────
	var newStatus string

	switch {
	case result.Passed(ruleAutoApprove):
		// Aprovação automática tem precedência sobre as demais
		slog.Info("▶ auto-approve", "id", contract.ID, "value", contract.Value)
		newStatus = "approved"

	case result.Passed(ruleNeedsCompliance) && result.Passed(ruleEligibleForCredit):
		// Elegível, mas valor alto → compliance obrigatório
		slog.Info("▶ compliance required", "id", contract.ID, "value", contract.Value,
			"threshold", limits.ComplianceMin)
		newStatus = "pending_compliance"

	case result.Passed(ruleEligibleForCredit):
		// Elegível e dentro do limite de crédito → aprovação normal
		slog.Info("▶ approved for credit", "id", contract.ID, "value", contract.Value,
			"limit", limits.MaxCreditValue)
		newStatus = "approved"

	case result.Failed(ruleEligibleForCredit) && result.Failed(ruleNeedsCompliance):
		// Produto PF ou valor acima do crédito máximo
		return fmt.Errorf("contract %q: product %q with value %.2f is not eligible",
			contract.ID, contract.Product, contract.Value)

	default:
		return fmt.Errorf("contract %q: no rule matched (product=%q, value=%.2f)",
			contract.ID, contract.Product, contract.Value)
	}

	// ── Persiste novo status ──────────────────────────────────────────────────
	contract.Status = newStatus
	contract.UpdatedAt = time.Now().UTC().Format(time.RFC3339)

	if err := contracts.PutItem(ctx, contract); err != nil {
		return fmt.Errorf("persist status update: %w", err)
	}

	// Invalida cache para que leituras futuras busquem o dado atualizado
	_ = cache.Delete(ctx, cacheKey)

	slog.Info("✓ contract processed",
		"id", contract.ID,
		"old_status", "pending",
		"new_status", newStatus,
	)
	return nil
}

// loadLimits carrega os parâmetros de negócio do SSM com fallbacks seguros.
func loadLimits(ctx context.Context, cfg *parameterstore.Block) AppLimits {
	getFloat := func(name string, fallback float64) float64 {
		raw, err := cfg.GetParameter(ctx, name)
		if err != nil {
			slog.Debug("SSM param not found, using fallback",
				"name", name, "fallback", fallback)
			return fallback
		}
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			slog.Warn("invalid SSM float, using fallback",
				"name", name, "raw", raw, "fallback", fallback)
			return fallback
		}
		return v
	}

	return AppLimits{
		MaxCreditValue: getFloat("contracts/max-credit-value", 100000.0),
		AutoApproveMax: getFloat("contracts/auto-approve-max", 5000.0),
		ComplianceMin:  getFloat("contracts/compliance-min", 50000.0),
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// Garante que o sample não quebre se core não for usado diretamente neste arquivo.
var _ = errors.New
var _ = core.ErrItemNotFound
