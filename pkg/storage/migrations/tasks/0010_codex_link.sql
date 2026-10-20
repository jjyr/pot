-- +migrate Up
-- +migrate StatementBegin
CREATE TABLE codex_link_assistants(
 id TEXT PRIMARY KEY,
 name TEXT NOT NULL,
 workspace TEXT NOT NULL,
 mode TEXT NOT NULL CHECK(mode IN ('read-only','workspace-write')),
 thread_id TEXT NOT NULL DEFAULT '',
 is_default INTEGER NOT NULL DEFAULT 0 CHECK(is_default IN (0,1))
);
CREATE UNIQUE INDEX codex_link_default ON codex_link_assistants(is_default) WHERE is_default=1;
CREATE TABLE codex_link_jobs(
 sequence INTEGER PRIMARY KEY AUTOINCREMENT,
 id TEXT NOT NULL UNIQUE,
 caller TEXT NOT NULL,
 request_id TEXT NOT NULL,
 assistant_id TEXT NOT NULL REFERENCES codex_link_assistants(id),
 payload TEXT NOT NULL,
 data TEXT NOT NULL,
 UNIQUE(caller,request_id)
);
-- +migrate StatementEnd
