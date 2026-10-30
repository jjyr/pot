-- +migrate Up
-- +migrate StatementBegin
UPDATE task_runs SET body=json_set(body,'$.schema_version',10) WHERE json_extract(body,'$.schema_version')=9;
CREATE TABLE task_sessions (
 control_id TEXT NOT NULL,
 task_id TEXT NOT NULL,
 sequence INTEGER NOT NULL DEFAULT 0,
 hash TEXT NOT NULL DEFAULT '',
 activity_at INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY(control_id,task_id)
);
-- +migrate StatementEnd
