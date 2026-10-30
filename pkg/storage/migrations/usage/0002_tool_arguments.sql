-- +migrate Up
-- +migrate StatementBegin
ALTER TABLE tools ADD COLUMN argument_bytes INTEGER CHECK(argument_bytes >= 0);
ALTER TABLE tools ADD COLUMN agent TEXT NOT NULL DEFAULT 'unknown';
CREATE INDEX tools_time ON tools(at,id);
CREATE INDEX tools_turn ON tools(scope,turn_id,at);
-- +migrate StatementEnd
