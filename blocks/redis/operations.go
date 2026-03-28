package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/raywall/go-code-blocks/core"
	"github.com/redis/go-redis/v9"
)

// ── String operations ────────────────────────────────────────────────────────

// Set stores value under key with an optional TTL (0 = no expiry).
func (b *Block) Set(ctx context.Context, key string, value any, ttl time.Duration) error {
	if err := b.checkInit(); err != nil {
		return err
	}
	if err := b.client.Set(ctx, b.prefixed(key), value, ttl).Err(); err != nil {
		return fmt.Errorf("redis %q set %q: %w", b.name, key, err)
	}
	return nil
}

// Get returns the string value associated with key.
// Returns core.ErrItemNotFound when the key does not exist.
func (b *Block) Get(ctx context.Context, key string) (string, error) {
	if err := b.checkInit(); err != nil {
		return "", err
	}
	val, err := b.client.Get(ctx, b.prefixed(key)).Result()
	if errors.Is(err, redis.Nil) {
		return "", fmt.Errorf("redis %q get %q: %w", b.name, key, core.ErrItemNotFound)
	}
	if err != nil {
		return "", fmt.Errorf("redis %q get %q: %w", b.name, key, err)
	}
	return val, nil
}

// SetJSON marshals v to JSON and stores it under key.
func (b *Block) SetJSON(ctx context.Context, key string, v any, ttl time.Duration) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("redis %q set-json %q: marshal: %w", b.name, key, err)
	}
	return b.Set(ctx, key, data, ttl)
}

// GetJSON retrieves the value at key and unmarshals it into v.
// Returns core.ErrItemNotFound when the key does not exist.
func (b *Block) GetJSON(ctx context.Context, key string, v any) error {
	raw, err := b.Get(ctx, key)
	if err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(raw), v); err != nil {
		return fmt.Errorf("redis %q get-json %q: unmarshal: %w", b.name, key, err)
	}
	return nil
}

// Delete removes one or more keys. Missing keys are silently ignored.
func (b *Block) Delete(ctx context.Context, keys ...string) error {
	if err := b.checkInit(); err != nil {
		return err
	}
	prefixed := make([]string, len(keys))
	for i, k := range keys {
		prefixed[i] = b.prefixed(k)
	}
	if err := b.client.Del(ctx, prefixed...).Err(); err != nil {
		return fmt.Errorf("redis %q delete: %w", b.name, err)
	}
	return nil
}

// Exists reports whether key is present in Redis.
func (b *Block) Exists(ctx context.Context, key string) (bool, error) {
	if err := b.checkInit(); err != nil {
		return false, err
	}
	n, err := b.client.Exists(ctx, b.prefixed(key)).Result()
	if err != nil {
		return false, fmt.Errorf("redis %q exists %q: %w", b.name, key, err)
	}
	return n > 0, nil
}

// Expire sets or refreshes the TTL on an existing key.
func (b *Block) Expire(ctx context.Context, key string, ttl time.Duration) error {
	if err := b.checkInit(); err != nil {
		return err
	}
	if err := b.client.Expire(ctx, b.prefixed(key), ttl).Err(); err != nil {
		return fmt.Errorf("redis %q expire %q: %w", b.name, key, err)
	}
	return nil
}

// ── Hash operations ──────────────────────────────────────────────────────────

// HSet sets one or more field-value pairs in the hash at key.
// fields should be passed as alternating field, value pairs:
//
//	block.HSet(ctx, "user:1", "name", "Alice", "email", "alice@example.com")
func (b *Block) HSet(ctx context.Context, key string, fields ...any) error {
	if err := b.checkInit(); err != nil {
		return err
	}
	if err := b.client.HSet(ctx, b.prefixed(key), fields...).Err(); err != nil {
		return fmt.Errorf("redis %q hset %q: %w", b.name, key, err)
	}
	return nil
}

// HGet retrieves a single field from the hash at key.
// Returns core.ErrItemNotFound when the key or field does not exist.
func (b *Block) HGet(ctx context.Context, key, field string) (string, error) {
	if err := b.checkInit(); err != nil {
		return "", err
	}
	val, err := b.client.HGet(ctx, b.prefixed(key), field).Result()
	if errors.Is(err, redis.Nil) {
		return "", fmt.Errorf("redis %q hget %q field %q: %w", b.name, key, field, core.ErrItemNotFound)
	}
	if err != nil {
		return "", fmt.Errorf("redis %q hget %q field %q: %w", b.name, key, field, err)
	}
	return val, nil
}

// HGetAll retrieves all fields and values from the hash at key.
func (b *Block) HGetAll(ctx context.Context, key string) (map[string]string, error) {
	if err := b.checkInit(); err != nil {
		return nil, err
	}
	result, err := b.client.HGetAll(ctx, b.prefixed(key)).Result()
	if err != nil {
		return nil, fmt.Errorf("redis %q hgetall %q: %w", b.name, key, err)
	}
	return result, nil
}

// ── helpers ──────────────────────────────────────────────────────────────────

func (b *Block) checkInit() error {
	if b.client == nil {
		return fmt.Errorf("redis %q: %w", b.name, core.ErrNotInitialized)
	}
	return nil
}

func (b *Block) prefixed(key string) string {
	if b.cfg.keyPrefix == "" {
		return key
	}
	return strings.TrimSuffix(b.cfg.keyPrefix, ":") + ":" + key
}
