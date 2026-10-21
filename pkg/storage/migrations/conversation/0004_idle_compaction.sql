-- +migrate Up
-- +migrate StatementBegin
CREATE TABLE assistant_idle_compaction(singleton INTEGER PRIMARY KEY CHECK(singleton=1),request_id TEXT NOT NULL DEFAULT '',finished_at INTEGER NOT NULL DEFAULT 0,route TEXT NOT NULL DEFAULT '',attempt_for TEXT NOT NULL DEFAULT '',prefix TEXT NOT NULL DEFAULT '{}');
INSERT INTO assistant_idle_compaction(singleton) VALUES(1);
-- +migrate StatementEnd
