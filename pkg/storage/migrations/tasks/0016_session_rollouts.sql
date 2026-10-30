-- +migrate Up
-- +migrate StatementBegin
-- Context remains inline until OpenJournal durably exports it and commits its
-- rollout reference. SQL migration never performs external filesystem effects.
UPDATE task_runs SET body=json_set(body,'$.schema_version',9) WHERE json_extract(body,'$.schema_version')=8;
CREATE TABLE task_rollout_migration (
 singleton INTEGER PRIMARY KEY CHECK(singleton=1),
 reclaim_pending INTEGER NOT NULL CHECK(reclaim_pending IN (0,1))
);
INSERT INTO task_rollout_migration(singleton,reclaim_pending)
SELECT 1,EXISTS(SELECT 1 FROM task_runs WHERE json_type(body,'$.context') IS NOT NULL AND json_type(body,'$.context')<>'null');
-- +migrate StatementEnd
