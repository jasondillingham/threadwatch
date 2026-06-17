// SPDX-License-Identifier: Apache-2.0

package storage_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasondillingham/threadwatch/internal/storage"
)

// openTestDB opens a fresh SQLite database in a temp dir and runs migrations.
func openTestDB(t *testing.T) *storage.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := storage.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestOpen_MigratesAndReopens(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "tw.db")

	db, err := storage.Open(ctx, path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	// A table-backed query proves the schema migrations ran.
	if _, err := db.ListThreads(ctx); err != nil {
		t.Fatalf("ListThreads after migrate: %v", err)
	}
	_ = db.Close()

	// Reopening the same file must be safe: migrations are idempotent.
	db2, err := storage.Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = db2.Close() })
	if _, err := db2.ListThreads(ctx); err != nil {
		t.Fatalf("ListThreads after reopen: %v", err)
	}
}

func TestUpsertThread_Idempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestDB(t)

	id1, err := db.UpsertThread(ctx, storage.Thread{
		Label: "A", Owner: "o", Repo: "r", Number: 1,
		Kind: "issue", State: "open", Title: "T",
	})
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	// Same (owner, repo, number): updates the label but must NOT downgrade a
	// known kind/state/title to unknown/empty, and must reuse the same row.
	id2, err := db.UpsertThread(ctx, storage.Thread{
		Label: "B", Owner: "o", Repo: "r", Number: 1,
		Kind: "unknown", State: "unknown", Title: "",
	})
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if id1 != id2 {
		t.Fatalf("upsert created a new row: id1=%d id2=%d", id1, id2)
	}

	got, err := db.GetThread(ctx, id1)
	if err != nil {
		t.Fatalf("GetThread: %v", err)
	}
	if got.Label != "B" {
		t.Errorf("label not updated: got %q, want B", got.Label)
	}
	if got.Kind != "issue" || got.State != "open" || got.Title != "T" {
		t.Errorf("known fields downgraded: kind=%q state=%q title=%q", got.Kind, got.State, got.Title)
	}
}

func TestInsertEventsIfNew_Dedup(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestDB(t)
	tid, err := db.UpsertThread(ctx, storage.Thread{Owner: "o", Repo: "r", Number: 1})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ev := func(kind, id string, offMin int) storage.NewEventRow {
		return storage.NewEventRow{
			SourceKind: kind, SourceID: id, EventType: "comment", Actor: "alice",
			OccurredAt: base.Add(time.Duration(offMin) * time.Minute), BodyExcerpt: "x", URL: "https://x",
		}
	}

	n, err := db.InsertEventsIfNew(ctx, tid, []storage.NewEventRow{
		ev("comment", "1", 1), ev("comment", "2", 2), ev("event", "9", 3),
	})
	if err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if n != 3 {
		t.Fatalf("first insert: got %d new, want 3", n)
	}

	// Re-inserting the same rows yields zero new (the unique constraint +
	// INSERT OR IGNORE is the dedup core).
	n, err = db.InsertEventsIfNew(ctx, tid, []storage.NewEventRow{ev("comment", "1", 1), ev("comment", "2", 2)})
	if err != nil {
		t.Fatalf("re-insert: %v", err)
	}
	if n != 0 {
		t.Fatalf("re-insert: got %d new, want 0", n)
	}

	// A mix of seen + new counts only the new one.
	n, err = db.InsertEventsIfNew(ctx, tid, []storage.NewEventRow{ev("comment", "2", 2), ev("comment", "3", 4)})
	if err != nil {
		t.Fatalf("mixed insert: %v", err)
	}
	if n != 1 {
		t.Fatalf("mixed insert: got %d new, want 1", n)
	}

	// Empty input is a no-op, not an error.
	if n, err := db.InsertEventsIfNew(ctx, tid, nil); err != nil || n != 0 {
		t.Fatalf("empty insert: n=%d err=%v", n, err)
	}

	seen, err := db.SeenIDs(ctx, tid)
	if err != nil {
		t.Fatalf("SeenIDs: %v", err)
	}
	for _, want := range []struct{ kind, id string }{
		{"comment", "1"}, {"comment", "2"}, {"comment", "3"}, {"event", "9"},
	} {
		if !seen[want.kind][want.id] {
			t.Errorf("SeenIDs missing %s/%s; got %+v", want.kind, want.id, seen)
		}
	}
	if seen["comment"]["999"] {
		t.Error("SeenIDs reported an id that was never inserted")
	}
}

func TestListEvents_NewestFirstAndLimit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestDB(t)
	tid, err := db.UpsertThread(ctx, storage.Thread{Owner: "o", Repo: "r", Number: 1})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var rows []storage.NewEventRow
	for i := 0; i < 3; i++ {
		rows = append(rows, storage.NewEventRow{
			SourceKind: "comment", SourceID: fmt.Sprint(i), EventType: "comment",
			OccurredAt: base.Add(time.Duration(i) * time.Hour),
		})
	}
	if _, err := db.InsertEventsIfNew(ctx, tid, rows); err != nil {
		t.Fatalf("insert: %v", err)
	}

	got, err := db.ListEvents(ctx, tid, 2)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("limit not honored: got %d, want 2", len(got))
	}
	// occurred_at DESC: newest (id "2") first, then "1".
	if got[0].SourceID != "2" || got[1].SourceID != "1" {
		t.Errorf("order: got %s,%s, want 2,1", got[0].SourceID, got[1].SourceID)
	}
}

func TestApplyThreadUpdate_NoDowngrade(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestDB(t)
	tid, err := db.UpsertThread(ctx, storage.Thread{
		Owner: "o", Repo: "r", Number: 1, Kind: "issue", State: "open", Title: "Orig",
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	ts := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	if err := db.ApplyThreadUpdate(ctx, tid, "closed", "New Title", "issue", &ts); err != nil {
		t.Fatalf("ApplyThreadUpdate: %v", err)
	}
	got, err := db.GetThread(ctx, tid)
	if err != nil {
		t.Fatalf("GetThread: %v", err)
	}
	if got.State != "closed" || got.Title != "New Title" {
		t.Errorf("update not applied: state=%q title=%q", got.State, got.Title)
	}
	if got.LastEventAt == nil || !got.LastEventAt.Equal(ts) {
		t.Errorf("LastEventAt: got %v, want %v", got.LastEventAt, ts)
	}

	// Empty/unknown values and a nil timestamp must preserve prior state.
	if err := db.ApplyThreadUpdate(ctx, tid, "unknown", "", "", nil); err != nil {
		t.Fatalf("second ApplyThreadUpdate: %v", err)
	}
	got2, err := db.GetThread(ctx, tid)
	if err != nil {
		t.Fatalf("GetThread: %v", err)
	}
	if got2.State != "closed" || got2.Title != "New Title" {
		t.Errorf("downgrade leaked: state=%q title=%q", got2.State, got2.Title)
	}
	if got2.LastEventAt == nil || !got2.LastEventAt.Equal(ts) {
		t.Errorf("LastEventAt cleared by nil update: %v", got2.LastEventAt)
	}
}

func TestPollOutcome_RoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestDB(t)
	tid, err := db.UpsertThread(ctx, storage.Thread{Owner: "o", Repo: "r", Number: 1})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	if err := db.SavePollOutcome(ctx, tid, "issue", storage.PollOutcome{ETag: `"abc"`, StatusCode: 200}); err != nil {
		t.Fatalf("save issue: %v", err)
	}
	if err := db.SavePollOutcome(ctx, tid, "comments", storage.PollOutcome{ETag: `"def"`, StatusCode: 200}); err != nil {
		t.Fatalf("save comments: %v", err)
	}

	etags, err := db.LoadETags(ctx, tid)
	if err != nil {
		t.Fatalf("LoadETags: %v", err)
	}
	if etags["issue"] != `"abc"` || etags["comments"] != `"def"` {
		t.Errorf("etags round-trip: got %+v", etags)
	}

	// Re-saving the same (thread, endpoint) updates the ETag in place.
	if err := db.SavePollOutcome(ctx, tid, "issue", storage.PollOutcome{ETag: `"xyz"`, StatusCode: 304}); err != nil {
		t.Fatalf("update issue: %v", err)
	}
	etags, err = db.LoadETags(ctx, tid)
	if err != nil {
		t.Fatalf("LoadETags after update: %v", err)
	}
	if etags["issue"] != `"xyz"` {
		t.Errorf("etag not updated: got %q, want \"xyz\"", etags["issue"])
	}
	if len(etags) != 2 {
		t.Errorf("endpoint count: got %d, want 2", len(etags))
	}
}
