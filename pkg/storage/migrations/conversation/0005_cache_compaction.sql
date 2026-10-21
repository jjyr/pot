-- +migrate Up
ALTER TABLE assistant_idle_compaction ADD COLUMN started_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE assistant_idle_compaction ADD COLUMN cache_read INTEGER;
ALTER TABLE assistant_idle_compaction ADD COLUMN cache_write INTEGER;
-- Old anchors have no cache evidence or request-start time. Wait for fresh usage.
UPDATE assistant_idle_compaction SET request_id='',attempt_for='',finished_at=0;
