-- +migrate Up
-- +migrate StatementBegin
CREATE TABLE IF NOT EXISTS task_run_history(assistant_id TEXT NOT NULL,task_id TEXT NOT NULL,run_id TEXT NOT NULL,body TEXT NOT NULL,PRIMARY KEY(assistant_id,task_id,run_id));
UPDATE tasks SET body=json_set(body,'$.schema_version',9,'$.info.run_id',json_extract(body,'$.run_id'),'$.info.archived',json(CASE WHEN phase IN ('complete','cancelled') THEN 'true' ELSE 'false' END))
 WHERE json_extract(body,'$.schema_version')=8;
-- +migrate StatementEnd
