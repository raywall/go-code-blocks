// samples/database/main.go
//
// Demonstra o uso do bloco DynamoDB de forma isolada.
//
// Operações cobertas:
//   - PutItem  — escrever um item
//   - GetItem  — ler por chave primária
//   - DeleteItem — remover por chave primária
//   - QueryItems — consulta paginada com expressão
//   - ScanItems  — varredura completa com paginação
//
// Variáveis de ambiente:
//
//	AWS_REGION       região AWS (default: us-east-1)
//	USERS_TABLE      nome da tabela (default: users-dev)
//	ORDERS_TABLE     nome da tabela de pedidos (default: orders-dev)
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/raywall/go-code-blocks/blocks/dynamodb"
	"github.com/raywall/go-code-blocks/core"
)

// ── Modelos ───────────────────────────────────────────────────────────────────

type User struct {
	ID        string `dynamodbav:"id"         json:"id"`
	Email     string `dynamodbav:"email"      json:"email"`
	Name      string `dynamodbav:"name"       json:"name"`
	CreatedAt string `dynamodbav:"created_at" json:"created_at"`
}

// Order usa chave composta (pk = user_id, sk = order_id).
type Order struct {
	UserID    string  `dynamodbav:"user_id"    json:"user_id"`
	OrderID   string  `dynamodbav:"order_id"   json:"order_id"`
	Total     float64 `dynamodbav:"total"      json:"total"`
	Status    string  `dynamodbav:"status"     json:"status"`
	CreatedAt string  `dynamodbav:"created_at" json:"created_at"`
}

func main() {
	ctx := context.Background()

	// ── Bloco: tabela de usuários (somente PK) ────────────────────────────────
	users := dynamodb.New[User]("users",
		dynamodb.WithRegion(envOr("AWS_REGION", "us-east-1")),
		dynamodb.WithTable(envOr("USERS_TABLE", "users-dev")),
		dynamodb.WithPartitionKey("id"),
	)

	// ── Bloco: tabela de pedidos (PK + SK) ────────────────────────────────────
	orders := dynamodb.New[Order]("orders",
		dynamodb.WithRegion(envOr("AWS_REGION", "us-east-1")),
		dynamodb.WithTable(envOr("ORDERS_TABLE", "orders-dev")),
		dynamodb.WithPartitionKey("user_id"),
		dynamodb.WithSortKey("order_id"),
	)

	app := core.NewContainer()
	app.MustRegister(users)
	app.MustRegister(orders)

	if err := app.InitAll(ctx); err != nil {
		slog.Error("init failed", "err", err)
		os.Exit(1)
	}
	defer app.ShutdownAll(ctx)

	// ── 1. Escrever usuários ──────────────────────────────────────────────────
	slog.Info("── PutItem ──")
	now := time.Now().UTC().Format(time.RFC3339)
	seeds := []User{
		{ID: "usr_001", Email: "alice@example.com", Name: "Alice", CreatedAt: now},
		{ID: "usr_002", Email: "bob@example.com", Name: "Bob", CreatedAt: now},
	}
	for _, u := range seeds {
		if err := users.PutItem(ctx, u); err != nil {
			slog.Error("PutItem", "id", u.ID, "err", err)
			continue
		}
		slog.Info("user written", "id", u.ID)
	}

	// ── 2. Ler usuário por PK ─────────────────────────────────────────────────
	slog.Info("── GetItem ──")
	u, err := users.GetItem(ctx, "usr_001", nil)
	if err != nil {
		slog.Error("GetItem", "err", err)
	} else {
		slog.Info("user fetched", "id", u.ID, "name", u.Name, "email", u.Email)
	}

	// ── 3. Escrever pedidos (chave composta) ──────────────────────────────────
	slog.Info("── PutItem (orders) ──")
	orderSeeds := []Order{
		{UserID: "usr_001", OrderID: "ord_A1", Total: 149.90, Status: "delivered", CreatedAt: now},
		{UserID: "usr_001", OrderID: "ord_A2", Total: 89.00, Status: "pending", CreatedAt: now},
		{UserID: "usr_001", OrderID: "ord_A3", Total: 210.50, Status: "cancelled", CreatedAt: now},
	}
	for _, o := range orderSeeds {
		if err := orders.PutItem(ctx, o); err != nil {
			slog.Error("PutItem order", "order_id", o.OrderID, "err", err)
		}
	}

	// ── 4. Ler pedido por chave composta ──────────────────────────────────────
	slog.Info("── GetItem (order with SK) ──")
	o, err := orders.GetItem(ctx, "usr_001", "ord_A1")
	if err != nil {
		slog.Error("GetItem order", "err", err)
	} else {
		slog.Info("order fetched", "order_id", o.OrderID, "total", o.Total, "status", o.Status)
	}

	// ── 5. Query paginada ─────────────────────────────────────────────────────
	slog.Info("── QueryItems ──")
	page, err := orders.QueryItems(ctx, dynamodb.QueryInput{
		KeyConditionExpression: "user_id = :uid AND begins_with(order_id, :prefix)",
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":uid":    mustMarshal("usr_001"),
			":prefix": mustMarshal("ord_"),
		},
		Limit: aws.Int32(10),
	})
	if err != nil {
		slog.Error("QueryItems", "err", err)
	} else {
		slog.Info("query result", "count", page.Count, "scanned", page.ScannedCount)
		for _, item := range page.Items {
			slog.Info("  order", "id", item.OrderID, "status", item.Status, "total", item.Total)
		}
	}

	// ── 6. Scan completo ──────────────────────────────────────────────────────
	slog.Info("── ScanItems ──")
	scanPage, err := users.ScanItems(ctx, aws.Int32(100), nil)
	if err != nil {
		slog.Error("ScanItems", "err", err)
	} else {
		slog.Info("scan result", "count", scanPage.Count)
		for _, item := range scanPage.Items {
			slog.Info("  user", "id", item.ID, "email", item.Email)
		}
	}

	// ── 7. Deletar usuário ────────────────────────────────────────────────────
	slog.Info("── DeleteItem ──")
	if err := users.DeleteItem(ctx, "usr_002", nil); err != nil {
		slog.Error("DeleteItem", "err", err)
	} else {
		slog.Info("user deleted", "id", "usr_002")
	}

	// Confirmar que foi removido
	_, err = users.GetItem(ctx, "usr_002", nil)
	if err != nil {
		slog.Info("confirmed: user no longer exists", "id", "usr_002", "err", err)
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// mustMarshal wraps attributevalue.Marshal para uso em ExpressionAttributeValues.
func mustMarshal(v any) types.AttributeValue {
	// import direto para manter o exemplo simples
	m, err := func() (types.AttributeValue, error) {
		switch val := v.(type) {
		case string:
			return &types.AttributeValueMemberS{Value: val}, nil
		case int, int32, int64, float32, float64:
			return &types.AttributeValueMemberN{Value: fmt.Sprintf("%v", val)}, nil
		default:
			return nil, fmt.Errorf("unsupported type %T", v)
		}
	}()
	if err != nil {
		panic(fmt.Sprintf("mustMarshal: %v", err))
	}
	return m
}
