-- +migrate Up
-- +migrate StatementBegin
CREATE UNIQUE INDEX IF NOT EXISTS task_events_task_sequence ON task_events(control_id,task_id,sequence);
UPDATE task_runs SET body=json_set(body,'$.schema_version',7) WHERE json_extract(body,'$.schema_version')=6;
-- +migrate StatementEnd
