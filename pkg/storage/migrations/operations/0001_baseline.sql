-- +migrate Up
-- +migrate StatementBegin
CREATE TABLE IF NOT EXISTS operations(id TEXT PRIMARY KEY,input_hash TEXT NOT NULL,state TEXT NOT NULL,result BLOB,error TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS operation_scopes(id TEXT PRIMARY KEY REFERENCES operations(id),scope TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS operation_inputs(id TEXT PRIMARY KEY REFERENCES operations(id),owner TEXT NOT NULL,turn_id TEXT NOT NULL,body TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS operation_input_run ON operation_inputs(owner,turn_id);
-- +migrate StatementEnd
