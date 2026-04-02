// blocks/rds/block.go
package rds

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/raywall/go-code-blocks/core"

	// Driver registration — blank imports so the user doesn't need to
	// import drivers manually in their own main package.
	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
)

// New creates a new RDS Block.
//
//	// PostgreSQL
//	db := rds.New("users-db",
//	    rds.WithDriver(rds.DriverPostgres),
//	    rds.WithHost("mydb.cluster-xyz.us-east-1.rds.amazonaws.com"),
//	    rds.WithPort(5432),
//	    rds.WithDatabase("myapp"),
//	    rds.WithUsername("app"),
//	    rds.WithPassword("secret"),
//	    rds.WithMaxOpenConns(20),
//	)
//
//	// Full DSN (e.g. secret fetched from Secrets Manager)
//	db := rds.New("users-db",
//	    rds.WithDriver(rds.DriverPostgres),
//	    rds.WithDSN(dsn),
//	)
func New(name string, opts ...Option) *Block {
	cfg := blockConfig{
		host:            "localhost",
		sslMode:         "require",
		maxOpenConns:    10,
		maxIdleConns:    5,
		connMaxLifetime: 5 * time.Minute,
		connMaxIdleTime: 1 * time.Minute,
		defaultTimeout:  30 * time.Second,
	}
	for _, o := range opts {
		o(&cfg)
	}
	return &Block{name: name, cfg: cfg}
}

// Name implements core.Block.
func (b *Block) Name() string { return b.name }

// Init implements core.Block. It opens the connection pool and verifies
// connectivity with a Ping.
func (b *Block) Init(ctx context.Context) error {
	dsn, err := b.buildDSN()
	if err != nil {
		return fmt.Errorf("rds %q: %w", b.name, err)
	}

	driverName := string(b.cfg.driver)
	db, err := sql.Open(driverName, dsn)
	if err != nil {
		return fmt.Errorf("rds %q: open: %w", b.name, err)
	}

	db.SetMaxOpenConns(b.cfg.maxOpenConns)
	db.SetMaxIdleConns(b.cfg.maxIdleConns)
	db.SetConnMaxLifetime(b.cfg.connMaxLifetime)
	db.SetConnMaxIdleTime(b.cfg.connMaxIdleTime)

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		db.Close()
		return fmt.Errorf("rds %q: ping: %w", b.name, err)
	}

	b.db = db
	return nil
}

// Shutdown implements core.Block. Closes all connections in the pool.
func (b *Block) Shutdown(_ context.Context) error {
	if b.db == nil {
		return nil
	}
	if err := b.db.Close(); err != nil {
		return fmt.Errorf("rds %q: close: %w", b.name, err)
	}
	b.db = nil
	return nil
}

// DB returns the underlying *sql.DB for advanced use cases (transactions,
// COPY, raw driver features). Prefer the typed helpers for regular queries.
func (b *Block) DB() *sql.DB { return b.db }

// checkInit returns core.ErrNotInitialized when the block has not been
// initialised via Init yet.
func (b *Block) checkInit() error {
	if b.db == nil {
		return fmt.Errorf("rds %q: %w", b.name, core.ErrNotInitialized)
	}
	return nil
}

// buildDSN constructs the driver-specific connection string from the
// individual options, or returns cfg.dsn directly when WithDSN was used.
func (b *Block) buildDSN() (string, error) {
	if b.cfg.dsn != "" {
		return b.cfg.dsn, nil
	}

	if b.cfg.driver == "" {
		return "", fmt.Errorf("driver not configured; use WithDriver or WithDSN")
	}

	host := b.cfg.host
	if host == "" {
		host = "localhost"
	}

	switch b.cfg.driver {
	case DriverPostgres:
		port := b.cfg.port
		if port == 0 {
			port = 5432
		}
		sslMode := b.cfg.sslMode
		if sslMode == "" {
			sslMode = "require"
		}
		return fmt.Sprintf(
			"postgres://%s:%s@%s:%d/%s?sslmode=%s",
			b.cfg.username, b.cfg.password,
			host, port,
			b.cfg.database, sslMode,
		), nil

	case DriverMySQL:
		port := b.cfg.port
		if port == 0 {
			port = 3306
		}
		return fmt.Sprintf(
			"%s:%s@tcp(%s:%d)/%s?parseTime=true&multiStatements=false",
			b.cfg.username, b.cfg.password,
			host, port,
			b.cfg.database,
		), nil

	default:
		return "", fmt.Errorf("unsupported driver %q; use DriverPostgres or DriverMySQL", b.cfg.driver)
	}
}

// Ensure Block implements core.Block.
var _ core.Block = (*Block)(nil)
