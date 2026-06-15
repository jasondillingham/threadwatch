// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Event is the projection used by HTTP handlers when rendering a thread's
// timeline. Fields map directly to the events table.
type Event struct {
	ID          int64
	ThreadID    int64
	SourceKind  string
	SourceID    string
	EventType   string
	Actor       string
	OccurredAt  time.Time
	BodyExcerpt string
	URL         string
}

// NewEventRow is the insert-side projection. RawJSON is optional; an
// empty value writes the table default ("{}").
type NewEventRow struct {
	SourceKind  string
	SourceID    string
	EventType   string
	Actor       string
	OccurredAt  time.Time
	BodyExcerpt string
	URL         string
	RawJSON     string
}

// SeenIDs returns the (source_kind -> set of source_id) map of events
// already recorded for the thread. Used to dedup during diff.
func (db *DB) SeenIDs(ctx context.Context, threadID int64) (map[string]map[string]bool, error) {
	rows, err := db.sql.QueryContext(ctx,
		`SELECT source_kind, source_id FROM events WHERE thread_id = ?`, threadID)
	if err != nil {
		return nil, fmt.Errorf("seen ids: %w", err)
	}
	defer rows.Close()

	out := map[string]map[string]bool{}
	for rows.Next() {
		var kind, id string
		if err := rows.Scan(&kind, &id); err != nil {
			return nil, err
		}
		if out[kind] == nil {
			out[kind] = map[string]bool{}
		}
		out[kind][id] = true
	}
	return out, rows.Err()
}

// InsertEventsIfNew inserts the supplied events for threadID using
// INSERT OR IGNORE on the (thread_id, source_kind, source_id) unique
// constraint. Returns the number of rows actually inserted (i.e., events
// the caller hadn't already seen).
//
// All inserts share a single transaction (in addition to any outer tx
// the caller may pass via the context-aware Tx variant).
func (db *DB) InsertEventsIfNew(ctx context.Context, threadID int64, events []NewEventRow) (int, error) {
	if len(events) == 0 {
		return 0, nil
	}
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT OR IGNORE INTO events
			(thread_id, source_kind, source_id, event_type, actor,
			 occurred_at, body_excerpt, url, raw_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return 0, fmt.Errorf("prepare events insert: %w", err)
	}
	defer stmt.Close()

	var inserted int
	for _, e := range events {
		raw := e.RawJSON
		if raw == "" {
			raw = "{}"
		}
		res, err := stmt.ExecContext(ctx,
			threadID, e.SourceKind, e.SourceID, e.EventType, e.Actor,
			e.OccurredAt.UTC().Format(time.RFC3339), e.BodyExcerpt, e.URL, raw,
		)
		if err != nil {
			return inserted, fmt.Errorf("insert event %s/%s: %w", e.SourceKind, e.SourceID, err)
		}
		n, _ := res.RowsAffected()
		inserted += int(n)
	}
	if err := tx.Commit(); err != nil {
		return inserted, fmt.Errorf("commit events: %w", err)
	}
	return inserted, nil
}

// ListEvents returns the most recent events for threadID, newest-first,
// capped at limit.
func (db *DB) ListEvents(ctx context.Context, threadID int64, limit int) ([]Event, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := db.sql.QueryContext(ctx, `
		SELECT id, thread_id, source_kind, source_id, event_type, actor,
		       occurred_at, body_excerpt, url
		FROM events
		WHERE thread_id = ?
		ORDER BY occurred_at DESC, id DESC
		LIMIT ?
	`, threadID, limit)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	defer rows.Close()

	var out []Event
	for rows.Next() {
		var e Event
		var occurred string
		if err := rows.Scan(
			&e.ID, &e.ThreadID, &e.SourceKind, &e.SourceID, &e.EventType,
			&e.Actor, &occurred, &e.BodyExcerpt, &e.URL,
		); err != nil {
			return nil, err
		}
		if t, err := time.Parse(time.RFC3339, occurred); err == nil {
			e.OccurredAt = t
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ApplyThreadUpdate writes the observed metadata back to the threads row.
// Empty/unknown values in update are *not* persisted (they preserve the
// previously-recorded value); this matters because a 304 on the issue
// endpoint leaves the snapshot's state/title/kind empty and we must not
// overwrite the row with empty strings.
//
// LastEventAt is written when non-nil (the diff guarantees it doesn't
// downgrade an existing timestamp), and skipped when nil.
func (db *DB) ApplyThreadUpdate(ctx context.Context, threadID int64, state, title, kind string, lastEventAt *time.Time) error {
	var lastEvent any = nil
	if lastEventAt != nil {
		lastEvent = lastEventAt.UTC().Format(time.RFC3339)
	}
	const q = `
		UPDATE threads SET
			state         = CASE WHEN ?2 = '' OR ?2 = 'unknown' THEN state ELSE ?2 END,
			title         = CASE WHEN ?3 = ''                  THEN title ELSE ?3 END,
			kind          = CASE WHEN ?4 = '' OR ?4 = 'unknown' THEN kind  ELSE ?4 END,
			last_event_at = COALESCE(?5, last_event_at),
			updated_at    = datetime('now')
		WHERE id = ?1
	`
	if _, err := db.sql.ExecContext(ctx, q, threadID, state, title, kind, lastEvent); err != nil {
		return fmt.Errorf("apply thread update %d: %w", threadID, err)
	}
	return nil
}

// silence unused import warning when EtagBucket helpers aren't needed.
var _ = sql.ErrNoRows
