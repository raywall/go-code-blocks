// blocks/rds/operations.go
package rds

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"time"
)

// ── QueryRows ─────────────────────────────────────────────────────────────────

// QueryRows executes a SELECT and returns every row as a []Row (map of
// column name → value). Useful for dynamic queries where the schema is not
// known at compile time.
//
//	rows, err := db.QueryRows(ctx,
//	    "SELECT id, name, email FROM users WHERE status = $1",
//	    "active",
//	)
func (b *Block) QueryRows(ctx context.Context, query string, args ...any) ([]Row, error) {
	if err := b.checkInit(); err != nil {
		return nil, err
	}

	ctx, cancel := b.withTimeout(ctx)
	defer cancel()
	rows, err := b.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("rds %q: query: %w", b.name, err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("rds %q: columns: %w", b.name, err)
	}

	var result []Row
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, fmt.Errorf("rds %q: scan: %w", b.name, err)
		}
		row := make(Row, len(cols))
		for i, col := range cols {
			row[col] = normalizeValue(vals[i])
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rds %q: rows: %w", b.name, err)
	}
	return result, nil
}

// ── QueryOne ──────────────────────────────────────────────────────────────────

// QueryOne executes a SELECT and scans the first row into dest using
// json-roundtrip mapping (column name → json tag on struct fields).
// Returns sql.ErrNoRows (wrapped) when nothing is found.
//
//	var user User
//	err := db.QueryOne(ctx, &user,
//	    "SELECT id, name, email FROM users WHERE id = $1", userID)
func (b *Block) QueryOne(ctx context.Context, dest any, query string, args ...any) error {
	if err := b.checkInit(); err != nil {
		return err
	}

	ctx, cancel := b.withTimeout(ctx)
	defer cancel()
	rows, err := b.db.QueryContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("rds %q: query: %w", b.name, err)
	}
	defer rows.Close()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return fmt.Errorf("rds %q: %w", b.name, err)
		}
		return fmt.Errorf("rds %q: %w", b.name, sql.ErrNoRows)
	}

	return b.scanInto(rows, dest)
}

// ── QueryAll ──────────────────────────────────────────────────────────────────

// QueryAll executes a SELECT and scans all rows into a slice of T.
// T must be a struct whose fields carry `db:` or `json:` tags matching
// the column names.
//
//	users, err := rds.QueryAll[User](ctx, db,
//	    "SELECT id, name, email FROM users WHERE active = $1", true)
func QueryAll[T any](ctx context.Context, b *Block, query string, args ...any) ([]T, error) {
	if err := b.checkInit(); err != nil {
		return nil, err
	}

	ctx, cancel := b.withTimeout(ctx)
	defer cancel()
	rows, err := b.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("rds %q: query: %w", b.name, err)
	}
	defer rows.Close()

	var result []T
	for rows.Next() {
		var item T
		if err := b.scanInto(rows, &item); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rds %q: rows: %w", b.name, err)
	}
	return result, nil
}

// ── QueryPage ─────────────────────────────────────────────────────────────────

// QueryPage executes a paginated SELECT and returns a Page[T].
// The query must include LIMIT and OFFSET placeholders as the last two args.
//
//	page, err := rds.QueryPage[User](ctx, db, 1, 20,
//	    "SELECT id, name FROM users WHERE active = $1", true)
func QueryPage[T any](ctx context.Context, b *Block, pageNum, pageSize int, query string, args ...any) (Page[T], error) {
	offset := (pageNum - 1) * pageSize
	pagedArgs := append(args, pageSize, offset)

	items, err := QueryAll[T](ctx, b, query+" LIMIT $"+argN(len(pagedArgs)-1)+" OFFSET $"+argN(len(pagedArgs)), pagedArgs...)
	if err != nil {
		return Page[T]{}, err
	}

	return Page[T]{
		Items:    items,
		Page:     pageNum,
		PageSize: pageSize,
		HasMore:  len(items) == pageSize,
	}, nil
}

// ── Exec ──────────────────────────────────────────────────────────────────────

// Exec executes an INSERT, UPDATE, DELETE or DDL statement.
// Returns the number of affected rows and the last insert ID (MySQL only;
// PostgreSQL should use RETURNING instead).
//
//	result, err := db.Exec(ctx,
//	    "UPDATE users SET status = $1 WHERE id = $2",
//	    "inactive", userID)
func (b *Block) Exec(ctx context.Context, query string, args ...any) (ExecResult, error) {
	if err := b.checkInit(); err != nil {
		return ExecResult{}, err
	}

	ctx, cancel := b.withTimeout(ctx)
	defer cancel()
	res, err := b.db.ExecContext(ctx, query, args...)
	if err != nil {
		return ExecResult{}, fmt.Errorf("rds %q: exec: %w", b.name, err)
	}

	affected, _ := res.RowsAffected()
	lastID, _ := res.LastInsertId()
	return ExecResult{RowsAffected: affected, LastInsertID: lastID}, nil
}

// ── Transaction ───────────────────────────────────────────────────────────────

// Tx executes fn inside a database transaction. If fn returns an error the
// transaction is rolled back; otherwise it is committed.
//
//	err := db.Tx(ctx, func(tx *sql.Tx) error {
//	    _, err := tx.ExecContext(ctx, "INSERT INTO orders ...")
//	    if err != nil { return err }
//	    _, err = tx.ExecContext(ctx, "UPDATE inventory ...")
//	    return err
//	})
func (b *Block) Tx(ctx context.Context, fn func(*sql.Tx) error) error {
	if err := b.checkInit(); err != nil {
		return err
	}

	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("rds %q: begin tx: %w", b.name, err)
	}

	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("rds %q: tx: %w", b.name, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("rds %q: commit: %w", b.name, err)
	}
	return nil
}

// ── Ping ──────────────────────────────────────────────────────────────────────

// Ping verifies the database connection is still alive.
// Useful for health checks.
func (b *Block) Ping(ctx context.Context) error {
	if err := b.checkInit(); err != nil {
		return err
	}
	if err := b.db.PingContext(ctx); err != nil {
		return fmt.Errorf("rds %q: ping: %w", b.name, err)
	}
	return nil
}

// ── Internal helpers ──────────────────────────────────────────────────────────

// withTimeout applies cfg.defaultTimeout to ctx when ctx has no deadline.
// The caller must always defer the returned cancel function to avoid context leaks.
func (b *Block) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {} // already has a deadline — no-op cancel
	}
	if b.cfg.defaultTimeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, b.cfg.defaultTimeout)
}

// scanInto scans the current row into dest via a JSON roundtrip so that
// struct fields with `json:"column_name"` tags are mapped automatically.
func (b *Block) scanInto(rows *sql.Rows, dest any) error {
	cols, err := rows.Columns()
	if err != nil {
		return fmt.Errorf("rds %q: columns: %w", b.name, err)
	}

	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		return fmt.Errorf("rds %q: scan: %w", b.name, err)
	}

	// Build a map and JSON-roundtrip into the struct.
	m := make(map[string]any, len(cols))
	for i, col := range cols {
		m[col] = normalizeValue(vals[i])
	}

	raw, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("rds %q: marshal: %w", b.name, err)
	}
	if err := json.Unmarshal(raw, dest); err != nil {
		return fmt.Errorf("rds %q: unmarshal into %T: %w", b.name, dest, err)
	}
	return nil
}

// normalizeValue converts driver-returned raw values ([]byte, time.Time, etc.)
// into JSON-friendly Go types.
func normalizeValue(v any) any {
	if v == nil {
		return nil
	}
	switch val := v.(type) {
	case []byte:
		return string(val)
	case time.Time:
		return val.UTC().Format(time.RFC3339Nano)
	default:
		// For numeric types returned as reflect.Value, return as-is.
		rv := reflect.ValueOf(v)
		switch rv.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			return rv.Int()
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			return rv.Uint()
		case reflect.Float32, reflect.Float64:
			return rv.Float()
		}
		return v
	}
}

// argN returns the positional placeholder for PostgreSQL ($1, $2, …).
func argN(n int) string {
	return fmt.Sprintf("%d", n)
}
