// blocks/rds/types.go
package rds

import (
	"database/sql"
	"time"
)

// Block is an RDS integration block. It manages a *sql.DB connection pool
// and exposes typed query helpers for PostgreSQL, MySQL and Aurora.
//
// The driver is selected via WithDriver; the connection string is built
// automatically from the individual options or supplied directly via WithDSN.
type Block struct {
	name string
	cfg  blockConfig
	db   *sql.DB
}

// ── Query result types ────────────────────────────────────────────────────────

// Row is a single result row represented as a map of column name → value.
// Values are raw []byte from the driver; use the typed helpers or
// Scan[T] for structured results.
type Row map[string]any

// Page is a paginated result set returned by QueryPage.
type Page[T any] struct {
	Items    []T
	Total    int64 // total rows matching the query (requires COUNT subquery)
	Page     int
	PageSize int
	HasMore  bool
}

// ExecResult wraps sql.Result for convenience.
type ExecResult struct {
	RowsAffected int64
	LastInsertID int64
}

// ── Driver constants ──────────────────────────────────────────────────────────

// Driver identifies the database engine.
type Driver string

const (
	// DriverPostgres targets PostgreSQL and Aurora PostgreSQL.
	// Uses github.com/lib/pq or pgx under the hood.
	DriverPostgres Driver = "postgres"

	// DriverMySQL targets MySQL and Aurora MySQL.
	// Uses github.com/go-sql-driver/mysql under the hood.
	DriverMySQL Driver = "mysql"
)

// ── blockConfig ───────────────────────────────────────────────────────────────

type blockConfig struct {
	driver   Driver
	dsn      string // full DSN — takes precedence over individual fields
	host     string
	port     int
	database string
	username string
	password string
	sslMode  string // postgres: disable|require|verify-ca|verify-full

	// Connection pool
	maxOpenConns    int
	maxIdleConns    int
	connMaxLifetime time.Duration
	connMaxIdleTime time.Duration

	// Query defaults
	defaultTimeout time.Duration
}
