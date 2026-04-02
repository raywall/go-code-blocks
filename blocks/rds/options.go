// blocks/rds/options.go
package rds

import "time"

// Option configures an RDS Block.
type Option func(*blockConfig)

// ── Driver and connection ─────────────────────────────────────────────────────

// WithDriver sets the database engine.
// Must be DriverPostgres or DriverMySQL. Required unless WithDSN is used.
func WithDriver(d Driver) Option {
	return func(c *blockConfig) { c.driver = d }
}

// WithDSN sets the full connection string, bypassing individual host/port/user
// options. Useful when the DSN is read from Secrets Manager or SSM at runtime.
//
//	// PostgreSQL
//	rds.WithDSN("postgres://user:pass@host:5432/dbname?sslmode=require")
//
//	// MySQL
//	rds.WithDSN("user:pass@tcp(host:3306)/dbname?parseTime=true")
func WithDSN(dsn string) Option {
	return func(c *blockConfig) { c.dsn = dsn }
}

// WithHost sets the database server hostname or IP.
// Defaults to "localhost".
func WithHost(host string) Option {
	return func(c *blockConfig) { c.host = host }
}

// WithPort sets the database server port.
// Defaults to 5432 for PostgreSQL and 3306 for MySQL.
func WithPort(port int) Option {
	return func(c *blockConfig) { c.port = port }
}

// WithDatabase sets the target database/schema name.
func WithDatabase(db string) Option {
	return func(c *blockConfig) { c.database = db }
}

// WithUsername sets the login username.
func WithUsername(user string) Option {
	return func(c *blockConfig) { c.username = user }
}

// WithPassword sets the login password.
func WithPassword(pass string) Option {
	return func(c *blockConfig) { c.password = pass }
}

// WithSSLMode sets the PostgreSQL SSL mode.
// Accepted values: disable, require, verify-ca, verify-full.
// Defaults to "require" for production safety.
func WithSSLMode(mode string) Option {
	return func(c *blockConfig) { c.sslMode = mode }
}

// ── Connection pool ───────────────────────────────────────────────────────────

// WithMaxOpenConns sets the maximum number of open connections.
// Defaults to 10.
func WithMaxOpenConns(n int) Option {
	return func(c *blockConfig) { c.maxOpenConns = n }
}

// WithMaxIdleConns sets the maximum number of idle connections kept in the pool.
// Defaults to 5.
func WithMaxIdleConns(n int) Option {
	return func(c *blockConfig) { c.maxIdleConns = n }
}

// WithConnMaxLifetime sets the maximum time a connection may be reused.
// Defaults to 5 minutes.
func WithConnMaxLifetime(d time.Duration) Option {
	return func(c *blockConfig) { c.connMaxLifetime = d }
}

// WithConnMaxIdleTime sets the maximum time a connection may remain idle.
// Defaults to 1 minute.
func WithConnMaxIdleTime(d time.Duration) Option {
	return func(c *blockConfig) { c.connMaxIdleTime = d }
}

// ── Query defaults ────────────────────────────────────────────────────────────

// WithQueryTimeout sets the default context timeout applied to every query
// that doesn't provide its own context deadline.
// Defaults to 30 seconds.
func WithQueryTimeout(d time.Duration) Option {
	return func(c *blockConfig) { c.defaultTimeout = d }
}
