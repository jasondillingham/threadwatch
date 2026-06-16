// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Thread is the projection used by HTTP handlers. Fields map 1:1 to the
// columns of the `threads` table.
type Thread struct {
	ID          int64
	Label       string
	Owner       string
	Repo        string
	Number      int
	Kind        string // "issue" | "pr" | "unknown"
	State       string // "open" | "closed" | "merged" | "unknown"
	Title       string
	LastEventAt *time.Time // nil until first poll surfaces an event
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// UpsertThread inserts a thread or, if (owner, repo, number) already exists,
// updates its label, kind, state, and title. Returns the resulting row's id.
//
// Used by the config-load path on startup to reconcile the live DB with the
// declared list. Idempotent; preserves last_event_at across calls.
func (db *DB) UpsertThread(ctx context.Context, t Thread) (int64, error) {
	const q = `
		INSERT INTO threads (label, owner, repo, number, kind, state, title)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(owner, repo, number) DO UPDATE SET
			label      = excluded.label,
			-- Don't downgrade a known kind/state/title to unknown/empty:
			kind       = CASE WHEN excluded.kind = 'unknown' THEN threads.kind  ELSE excluded.kind  END,
			state      = CASE WHEN excluded.state = 'unknown' THEN threads.state ELSE excluded.state END,
			title      = CASE WHEN excluded.title = ''        THEN threads.title ELSE excluded.title END,
			updated_at = datetime('now')
		RETURNING id
	`
	var id int64
	err := db.sql.QueryRowContext(ctx, q,
		t.Label, t.Owner, t.Repo, t.Number, defaultKind(t.Kind), defaultState(t.State), t.Title,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("upsert thread %s/%s#%d: %w", t.Owner, t.Repo, t.Number, err)
	}
	return id, nil
}

// ListThreads returns all configured threads ordered by id (insertion order).
// Until the poller is added (Checkpoint C), LastEventAt will be nil for every
// row.
func (db *DB) ListThreads(ctx context.Context) ([]Thread, error) {
	const q = `
		SELECT id, label, owner, repo, number, kind, state, title,
		       last_event_at, created_at, updated_at
		FROM threads
		ORDER BY id ASC
	`
	rows, err := db.sql.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list threads: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Thread
	for rows.Next() {
		var t Thread
		var lastEvent sql.NullString
		var createdAt, updatedAt string
		if err := rows.Scan(
			&t.ID, &t.Label, &t.Owner, &t.Repo, &t.Number, &t.Kind, &t.State, &t.Title,
			&lastEvent, &createdAt, &updatedAt,
		); err != nil {
			return nil, err
		}
		if lastEvent.Valid {
			tm, parseErr := time.Parse(time.DateTime, lastEvent.String)
			if parseErr == nil {
				t.LastEventAt = &tm
			}
		}
		t.CreatedAt = parseSQLiteTime(createdAt)
		t.UpdatedAt = parseSQLiteTime(updatedAt)
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// GetThread fetches a thread by id.
func (db *DB) GetThread(ctx context.Context, id int64) (Thread, error) {
	const q = `
		SELECT id, label, owner, repo, number, kind, state, title,
		       last_event_at, created_at, updated_at
		FROM threads
		WHERE id = ?
	`
	var t Thread
	var lastEvent sql.NullString
	var createdAt, updatedAt string
	err := db.sql.QueryRowContext(ctx, q, id).Scan(
		&t.ID, &t.Label, &t.Owner, &t.Repo, &t.Number, &t.Kind, &t.State, &t.Title,
		&lastEvent, &createdAt, &updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Thread{}, ErrNotFound
	}
	if err != nil {
		return Thread{}, err
	}
	if lastEvent.Valid {
		tm, parseErr := time.Parse(time.DateTime, lastEvent.String)
		if parseErr == nil {
			t.LastEventAt = &tm
		}
	}
	t.CreatedAt = parseSQLiteTime(createdAt)
	t.UpdatedAt = parseSQLiteTime(updatedAt)
	return t, nil
}

// ErrNotFound is returned by the *Get* helpers when the row is missing.
var ErrNotFound = errors.New("storage: not found")

func defaultKind(k string) string {
	switch k {
	case "issue", "pr", "unknown":
		return k
	default:
		return "unknown"
	}
}

func defaultState(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

func parseSQLiteTime(s string) time.Time {
	if t, err := time.Parse(time.DateTime, s); err == nil {
		return t
	}
	return time.Time{}
}
