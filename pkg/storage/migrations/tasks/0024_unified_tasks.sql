-- +migrate Up
-- +migrate StatementBegin
UPDATE tasks SET body=json_set(body,'$.schema_version',12) WHERE json_extract(body,'$.schema_version')=11;
UPDATE task_run_history SET body=json_set(body,'$.schema_version',12) WHERE json_extract(body,'$.schema_version')=11;
ALTER TABLE task_outcomes ADD COLUMN run_id TEXT NOT NULL DEFAULT '';
-- Backfill only the latest existing outcome for a currently ended local run.
UPDATE task_outcomes AS o SET run_id=COALESCE((SELECT json_extract(t.body,'$.run_id') FROM tasks t WHERE t.assistant_id=o.assistant_id AND t.id=o.task_id AND t.phase=o.phase),'')
WHERE o.sequence=(SELECT MAX(p.sequence) FROM task_outcomes p WHERE p.assistant_id=o.assistant_id AND p.task_id=o.task_id);
CREATE UNIQUE INDEX task_outcomes_run ON task_outcomes(assistant_id,task_id,run_id,phase) WHERE run_id!='';
CREATE TABLE task_submission_import(singleton INTEGER PRIMARY KEY CHECK(singleton=1));
INSERT INTO task_submission_import VALUES(1);
-- +migrate StatementEnd
