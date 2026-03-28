package redis

import (
	"crypto/tls"
	"time"
)

// Option configures a Redis Block.
type Option func(*blockConfig)

type blockConfig struct {
	addr         string
	password     string
	db           int
	dialTimeout  time.Duration
	readTimeout  time.Duration
	writeTimeout time.Duration
	poolSize     int
	tlsConfig    *tls.Config
	keyPrefix    string
}

// ── Connection ───────────────────────────────────────────────────────────────

// WithAddr sets the Redis server address (default: "localhost:6379").
func WithAddr(addr string) Option {
	return func(c *blockConfig) { c.addr = addr }
}

// WithPassword sets the Redis AUTH password.
func WithPassword(password string) Option {
	return func(c *blockConfig) { c.password = password }
}

// WithDB selects the Redis logical database index (default: 0).
func WithDB(db int) Option {
	return func(c *blockConfig) { c.db = db }
}

// WithTLS configures TLS for the Redis connection.
func WithTLS(tlsCfg *tls.Config) Option {
	return func(c *blockConfig) { c.tlsConfig = tlsCfg }
}

// WithPoolSize sets the maximum number of idle connections in the pool.
func WithPoolSize(n int) Option {
	return func(c *blockConfig) { c.poolSize = n }
}

// WithDialTimeout sets the timeout for establishing new connections.
func WithDialTimeout(d time.Duration) Option {
	return func(c *blockConfig) { c.dialTimeout = d }
}

// WithReadTimeout sets the per-command read deadline.
func WithReadTimeout(d time.Duration) Option {
	return func(c *blockConfig) { c.readTimeout = d }
}

// WithWriteTimeout sets the per-command write deadline.
func WithWriteTimeout(d time.Duration) Option {
	return func(c *blockConfig) { c.writeTimeout = d }
}

// ── Keys ─────────────────────────────────────────────────────────────────────

// WithKeyPrefix prepends a namespace to every key used by this block.
// Useful for isolating environments (e.g. "prod:", "staging:").
func WithKeyPrefix(prefix string) Option {
	return func(c *blockConfig) { c.keyPrefix = prefix }
}
