-- +migrate Up
-- +migrate StatementBegin
-- First public schema baseline; pre-release databases are intentionally unsupported.
CREATE TABLE operation_inputs(id TEXT PRIMARY KEY REFERENCES operations(id),owner TEXT NOT NULL,turn_id TEXT NOT NULL,body TEXT NOT NULL);

CREATE TABLE operation_scopes(id TEXT PRIMARY KEY REFERENCES operations(id),scope TEXT NOT NULL);

CREATE TABLE operations(id TEXT PRIMARY KEY,input_hash TEXT NOT NULL,state TEXT NOT NULL,result BLOB,error TEXT NOT NULL);

CREATE INDEX operation_input_run ON operation_inputs(owner,turn_id);

-- +migrate StatementEnd
