CREATE INDEX idx_events_thread_time ON events(thread_id, occurred_at DESC);
CREATE INDEX idx_events_inserted    ON events(inserted_at DESC);
CREATE INDEX idx_threads_last_event ON threads(last_event_at DESC);
