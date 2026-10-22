-- +migrate Up
-- +migrate StatementBegin
-- Explicit tasks own their thread and execution scope; assistant message history
-- retains its original thread. No shared thread is assigned to a new task.
DROP TABLE codex_approval_peers;
CREATE TABLE codex_task_executor (
 singleton INTEGER PRIMARY KEY CHECK(singleton=1),
 workspace TEXT NOT NULL,
 mode TEXT NOT NULL CHECK(mode IN ('read-only','workspace-write'))
);
CREATE TABLE codex_link_jobs_next (
 sequence INTEGER PRIMARY KEY AUTOINCREMENT,
 id TEXT NOT NULL UNIQUE,
 caller TEXT NOT NULL,
 request_id TEXT NOT NULL,
 assistant_id TEXT REFERENCES codex_link_assistants(id),
 payload TEXT NOT NULL,
 data TEXT NOT NULL,
 UNIQUE(caller,request_id)
);
INSERT INTO codex_link_jobs_next SELECT * FROM codex_link_jobs;
DROP TABLE codex_link_jobs;
ALTER TABLE codex_link_jobs_next RENAME TO codex_link_jobs;
CREATE TABLE codex_task_sessions (
 task_id TEXT PRIMARY KEY REFERENCES codex_link_jobs(id),
 workspace TEXT NOT NULL,
 mode TEXT NOT NULL CHECK(mode IN ('read-only','workspace-write')),
 thread_id TEXT NOT NULL DEFAULT ''
);
CREATE TABLE codex_task_inputs (
 task_id TEXT NOT NULL REFERENCES codex_link_jobs(id),
 request_id TEXT NOT NULL,
 text TEXT NOT NULL,
 previous_run TEXT NOT NULL,
 PRIMARY KEY(task_id,request_id)
);
-- +migrate StatementEnd
