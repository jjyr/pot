-- +migrate Up
-- +migrate StatementBegin
-- Historical updated timestamps can include later metadata writes. Keep unknown
-- finish times unknown, including archived runs, without replaying any work.
UPDATE tasks SET body=json_set(body,'$.schema_version',14,'$.info.finished_at_ms',0) WHERE json_extract(body,'$.schema_version')=13;
UPDATE task_run_history SET body=json_set(body,'$.schema_version',14,'$.info.finished_at_ms',0) WHERE json_extract(body,'$.schema_version')=13;
UPDATE task_runs SET body=json_set(body,'$.schema_version',8,'$.finished_at_ms',0) WHERE json_extract(body,'$.schema_version')=7;
-- +migrate StatementEnd
