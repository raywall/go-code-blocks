// samples/cache/main.go
//
// Demonstra o uso do bloco Redis de forma isolada.
//
// Operações cobertas:
//   - Set / Get          — strings simples com TTL
//   - SetJSON / GetJSON  — serialização de structs
//   - Delete / Exists    — gerenciamento de chaves
//   - Expire             — renovação de TTL
//   - HSet / HGet / HGetAll — operações em hash (ideal para sessões)
//
// Variáveis de ambiente:
//
//	REDIS_ADDR      endereço do servidor (default: localhost:6379)
//	REDIS_PASSWORD  senha (default: vazio)
//	REDIS_DB        database index (default: 0)
package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/raywall/go-code-blocks/blocks/redis"
	"github.com/raywall/go-code-blocks/core"
)

// ── Modelos ───────────────────────────────────────────────────────────────────

type UserProfile struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Email     string   `json:"email"`
	Roles     []string `json:"roles"`
	UpdatedAt string   `json:"updated_at"`
}

type CartItem struct {
	ProductID string  `json:"product_id"`
	Name      string  `json:"name"`
	Qty       int     `json:"qty"`
	Price     float64 `json:"price"`
}

func main() {
	ctx := context.Background()

	db, _ := strconv.Atoi(envOr("REDIS_DB", "0"))

	cache := redis.New("cache",
		redis.WithAddr(envOr("REDIS_ADDR", "localhost:6379")),
		redis.WithPassword(envOr("REDIS_PASSWORD", "")),
		redis.WithDB(db),
		redis.WithKeyPrefix("myapp:"), // todo key recebe o prefixo automaticamente
		redis.WithDialTimeout(3*time.Second),
		redis.WithReadTimeout(2*time.Second),
		redis.WithWriteTimeout(2*time.Second),
		redis.WithPoolSize(10),
	)

	app := core.NewContainer()
	app.MustRegister(cache)

	if err := app.InitAll(ctx); err != nil {
		slog.Error("init failed", "err", err)
		os.Exit(1)
	}
	defer app.ShutdownAll(ctx)

	// ── 1. String simples com TTL ─────────────────────────────────────────────
	slog.Info("── Set / Get ──")
	if err := cache.Set(ctx, "greeting", "Olá, mundo!", 10*time.Second); err != nil {
		slog.Error("Set", "err", err)
	}
	val, err := cache.Get(ctx, "greeting")
	if err != nil {
		slog.Error("Get", "err", err)
	} else {
		slog.Info("got value", "key", "greeting", "value", val)
	}

	// ── 2. Struct serializado como JSON ───────────────────────────────────────
	slog.Info("── SetJSON / GetJSON ──")
	profile := UserProfile{
		ID:        "usr_001",
		Name:      "Alice",
		Email:     "alice@example.com",
		Roles:     []string{"admin", "editor"},
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if err := cache.SetJSON(ctx, "profile:usr_001", profile, 5*time.Minute); err != nil {
		slog.Error("SetJSON", "err", err)
	}

	var fetched UserProfile
	if err := cache.GetJSON(ctx, "profile:usr_001", &fetched); err != nil {
		slog.Error("GetJSON", "err", err)
	} else {
		slog.Info("profile from cache", "id", fetched.ID, "name", fetched.Name, "roles", fetched.Roles)
	}

	// ── 3. Exists ─────────────────────────────────────────────────────────────
	slog.Info("── Exists ──")
	exists, err := cache.Exists(ctx, "profile:usr_001")
	if err != nil {
		slog.Error("Exists", "err", err)
	} else {
		slog.Info("key exists?", "key", "profile:usr_001", "exists", exists)
	}

	// ── 4. Renovar TTL de uma chave existente ─────────────────────────────────
	slog.Info("── Expire ──")
	if err := cache.Expire(ctx, "profile:usr_001", 30*time.Minute); err != nil {
		slog.Error("Expire", "err", err)
	} else {
		slog.Info("TTL renewed", "key", "profile:usr_001", "new_ttl", "30m")
	}

	// ── 5. Carrinho de compras com múltiplos itens ───────────────────────────
	slog.Info("── SetJSON (cart items) ──")
	cart := []CartItem{
		{ProductID: "prd_1", Name: "Teclado Mecânico", Qty: 1, Price: 349.90},
		{ProductID: "prd_2", Name: "Mouse Gamer", Qty: 2, Price: 199.00},
	}
	if err := cache.SetJSON(ctx, "cart:usr_001", cart, 1*time.Hour); err != nil {
		slog.Error("SetJSON cart", "err", err)
	}

	var cartFromCache []CartItem
	if err := cache.GetJSON(ctx, "cart:usr_001", &cartFromCache); err != nil {
		slog.Error("GetJSON cart", "err", err)
	} else {
		for _, item := range cartFromCache {
			slog.Info("cart item", "product", item.Name, "qty", item.Qty, "price", item.Price)
		}
	}

	// ── 6. Hash — ideal para sessões e objetos parcialmente atualizáveis ──────
	slog.Info("── HSet / HGet / HGetAll ──")
	sessionKey := "session:tok_abc123"

	if err := cache.HSet(ctx, sessionKey,
		"uid", "usr_001",
		"role", "admin",
		"ip", "192.168.1.10",
		"created_at", time.Now().Unix(),
	); err != nil {
		slog.Error("HSet", "err", err)
	}
	if err := cache.Expire(ctx, sessionKey, 24*time.Hour); err != nil {
		slog.Error("Expire session", "err", err)
	}

	role, err := cache.HGet(ctx, sessionKey, "role")
	if err != nil {
		slog.Error("HGet", "err", err)
	} else {
		slog.Info("session field", "role", role)
	}

	allFields, err := cache.HGetAll(ctx, sessionKey)
	if err != nil {
		slog.Error("HGetAll", "err", err)
	} else {
		slog.Info("session data", "fields", allFields)
	}

	// ── 7. Chave inexistente — erro tipado ────────────────────────────────────
	slog.Info("── ErrItemNotFound ──")
	_, err = cache.Get(ctx, "this:key:does:not:exist")
	if errors.Is(err, core.ErrItemNotFound) {
		slog.Info("key not found (expected)", "err", err)
	} else if err != nil {
		slog.Error("unexpected error", "err", err)
	}

	// ── 8. Delete em lote ─────────────────────────────────────────────────────
	slog.Info("── Delete ──")
	if err := cache.Delete(ctx, "greeting", "profile:usr_001", "cart:usr_001"); err != nil {
		slog.Error("Delete", "err", err)
	} else {
		slog.Info("keys deleted", "count", 3)
	}

	exists, _ = cache.Exists(ctx, "profile:usr_001")
	slog.Info("after delete, key exists?", "key", "profile:usr_001", "exists", exists)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
