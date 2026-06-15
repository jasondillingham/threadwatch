// SPDX-License-Identifier: Apache-2.0

// Package storage owns the SQLite database, schema migrations, and the
// small set of CRUD helpers the rest of threadwatch needs.
package storage

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"time"

	// modernc.org/sqlite is a pure-Go SQLite implementation. No cgo, so the
	// container image stays distroless-static-friendly.
	_ "modernc.org/sqlite"
)

// DB wraps *sql.DB and the package's helpers hang off it. Open via Open().
type DB struct {
	sql *sql.DB
}

// Open opens (creating if necessary) the SQLite database at path and runs
// any pending migrations. It enables WAL, foreign keys, and a reasonable
// busy timeout so readers don't immediately fail when the writer holds the
// lock.
func Open(ctx context.Context, path string) (*DB, error) {
	dsn := buildDSN(path)
	sdb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	// SQLite likes a single connection for writes; many readers are fine.
	// We use a small pool to keep things predictable.
	sdb.SetMaxOpenConns(8)
	sdb.SetMaxIdleConns(4)
	sdb.SetConnMaxLifetime(0)

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := sdb.PingContext(pingCtx); err != nil {
		_ = sdb.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	db := &DB{sql: sdb}
	if err := db.migrate(ctx); err != nil {
		_ = sdb.Close()
		return nil, err
	}
	return db, nil
}

// Close releases the underlying sql.DB.
func (db *DB) Close() error { return db.sql.Close() }

// SQL returns the underlying *sql.DB. Reserved for tests and callers that
// genuinely need raw access (the migration runner uses it).
func (db *DB) SQL() *sql.DB { return db.sql }

func buildDSN(path string) string {
	q := url.Values{}
	q.Set("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "foreign_keys(on)")
	q.Add("_pragma", "synchronous(NORMAL)")
	return "file:" + path + "?" + q.Encode()
}
