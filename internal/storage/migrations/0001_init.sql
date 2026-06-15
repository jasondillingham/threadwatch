-- threadwatch schema, initial.
--
-- One database, one writer (the poller), many readers (HTTP handlers).
-- WAL mode and SQLite's locking semantics make this safe.
--
-- The schema_migrations table is created by the runner (not here) so the
-- runner can read its state before applying any migration.

CREATE TABLE threads (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  label         TEXT NOT NULL,                             -- human label
  owner         TEXT NOT NULL,                             -- "tailscale"
  repo          TEXT NOT NULL,                             -- "tailscale"
  number        INTEGER NOT NULL,                          -- 19938
  kind          TEXT NOT NULL DEFAULT 'unknown'
                  CHECK (kind IN ('issue','pr','unknown')),
  state         TEXT NOT NULL DEFAULT 'unknown',           -- open|closed|merged|unknown
  title         TEXT NOT NULL DEFAULT '',
  last_event_at TEXT,                                       -- ISO8601; null until first poll
  created_at    TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at    TEXT NOT NULL DEFAULT (datetime('now')),
  UNIQUE(owner, repo, number)
);

CREATE TABLE events (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  thread_id    INTEGER NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
  source_kind  TEXT NOT NULL,                              -- issue_comment|review|review_comment|issue_event|state_change
  source_id    TEXT NOT NULL,                              -- GH numeric id as text; state_change synthesizes one
  event_type   TEXT NOT NULL,                              -- commented|reviewed|approved|labeled|closed|merged|...
  actor        TEXT NOT NULL DEFAULT '',
  occurred_at  TEXT NOT NULL,                              -- ISO8601 from GitHub
  body_excerpt TEXT NOT NULL DEFAULT '',                   -- first ~280 chars for timeline preview
  url          TEXT NOT NULL DEFAULT '',
  raw_json     TEXT NOT NULL DEFAULT '{}',                 -- the source record for forensics
  inserted_at  TEXT NOT NULL DEFAULT (datetime('now')),
  UNIQUE(thread_id, source_kind, source_id)
);

CREATE TABLE polls (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  thread_id      INTEGER NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
  endpoint       TEXT NOT NULL,                             -- "issue"|"comments"|"events"|"reviews"|"review_comments"
  etag           TEXT NOT NULL DEFAULT '',                  -- last seen ETag for If-None-Match
  last_status    INTEGER NOT NULL DEFAULT 0,                -- HTTP status of last poll
  last_polled_at TEXT NOT NULL DEFAULT (datetime('now')),
  last_error     TEXT NOT NULL DEFAULT '',
  UNIQUE(thread_id, endpoint)
);
