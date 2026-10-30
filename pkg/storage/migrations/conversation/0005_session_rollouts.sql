-- +migrate Up
-- +migrate StatementBegin
ALTER TABLE context_history RENAME TO rollout_legacy_context;
CREATE TABLE context_rollout(singleton INTEGER PRIMARY KEY CHECK(singleton=1),sequence INTEGER NOT NULL DEFAULT 0,hash TEXT NOT NULL DEFAULT '',imported INTEGER NOT NULL DEFAULT 0,reclaim_pending INTEGER NOT NULL DEFAULT 0);
INSERT INTO context_rollout(singleton) VALUES(1);
-- +migrate StatementEnd
