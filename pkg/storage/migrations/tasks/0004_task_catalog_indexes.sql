-- +migrate Up
-- +migrate StatementBegin
CREATE INDEX IF NOT EXISTS tasks_catalog_order ON tasks(CASE WHEN json_valid(body) THEN json_extract(body,'$.info.updated_at_ms') END DESC,id,assistant_id);
CREATE INDEX IF NOT EXISTS tasks_owner_order ON tasks(assistant_id,CASE WHEN json_valid(body) THEN json_extract(body,'$.info.updated_at_ms') END DESC,id);
CREATE INDEX IF NOT EXISTS tasks_catalog_phase ON tasks(phase);
-- +migrate StatementEnd
