// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"context"
	"fmt"
)

// PollOutcome is what gets recorded into the polls table after one
// conditional GET against a thread+endpoint combination.
type PollOutcome struct {
	ETag       string
	StatusCode int
	Err        string
}

// LoadETags returns the per-endpoint ETag bookkeeping for threadID,
// keyed by endpoint name (the github package's Endpoint* constants).
func (db *DB) LoadETags(ctx context.Context, threadID int64) (map[string]string, error) {
	rows, err := db.sql.QueryContext(ctx,
		`SELECT endpoint, etag FROM polls WHERE thread_id = ?`, threadID)
	if err != nil {
		return nil, fmt.Errorf("load etags: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := map[string]string{}
	for rows.Next() {
		var endpoint, etag string
		if err := rows.Scan(&endpoint, &etag); err != nil {
			return nil, err
		}
		out[endpoint] = etag
	}
	return out, rows.Err()
}

// SavePollOutcome upserts the (thread_id, endpoint) row in polls. ETag,
// status, and last_error are recorded; last_polled_at is set to now via
// the table default semantics on insert and explicitly on update.
func (db *DB) SavePollOutcome(ctx context.Context, threadID int64, endpoint string, out PollOutcome) error {
	const q = `
		INSERT INTO polls (thread_id, endpoint, etag, last_status, last_error)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(thread_id, endpoint) DO UPDATE SET
			etag           = excluded.etag,
			last_status    = excluded.last_status,
			last_polled_at = datetime('now'),
			last_error     = excluded.last_error
	`
	if _, err := db.sql.ExecContext(ctx, q, threadID, endpoint, out.ETag, out.StatusCode, out.Err); err != nil {
		return fmt.Errorf("save poll outcome %d/%s: %w", threadID, endpoint, err)
	}
	return nil
}
