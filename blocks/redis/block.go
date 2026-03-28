// Package redis provides a Redis integration block with a typed caching API.
package redis

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// Block is a Redis integration block.
type Block struct {
	name   string
	cfg    blockConfig
	client *redis.Client
}

// New creates a new Redis Block.
//
//	block := redis.New("cache",
//	    redis.WithAddr("localhost:6379"),
//	    redis.WithPassword("secret"),
//	    redis.WithDB(0),
//	    redis.WithKeyPrefix("myapp:"),
//	)
func New(name string, opts ...Option) *Block {
	cfg := blockConfig{
		addr: "localhost:6379",
	}
	for _, o := range opts {
		o(&cfg)
	}
	return &Block{name: name, cfg: cfg}
}

// Name implements core.Block.
func (b *Block) Name() string { return b.name }

// Init implements core.Block. It creates the Redis client and verifies
// connectivity with a PING command.
func (b *Block) Init(ctx context.Context) error {
	opts := &redis.Options{
		Addr:      b.cfg.addr,
		Password:  b.cfg.password,
		DB:        b.cfg.db,
		TLSConfig: b.cfg.tlsConfig,
	}
	if b.cfg.poolSize > 0 {
		opts.PoolSize = b.cfg.poolSize
	}
	if b.cfg.dialTimeout > 0 {
		opts.DialTimeout = b.cfg.dialTimeout
	}
	if b.cfg.readTimeout > 0 {
		opts.ReadTimeout = b.cfg.readTimeout
	}
	if b.cfg.writeTimeout > 0 {
		opts.WriteTimeout = b.cfg.writeTimeout
	}

	b.client = redis.NewClient(opts)

	if err := b.client.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("redis %q: ping: %w", b.name, err)
	}
	return nil
}

// Shutdown implements core.Block. Closes the connection pool gracefully.
func (b *Block) Shutdown(_ context.Context) error {
	if b.client != nil {
		return b.client.Close()
	}
	return nil
}
