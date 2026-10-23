-- +migrate Up
-- +migrate StatementBegin
-- Historical authority is evidence, not a coordinator/runner configuration.
ALTER TABLE task_authority RENAME TO retired_task_authority;
CREATE TABLE task_node(singleton INTEGER PRIMARY KEY CHECK(singleton=1),node_id TEXT NOT NULL);
INSERT INTO task_node(singleton,node_id) SELECT 1,control_id FROM retired_task_authority;
-- +migrate StatementEnd
