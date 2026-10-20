-- +migrate Up
-- +migrate StatementBegin
ALTER TABLE assistant_definitions ADD COLUMN deleted INTEGER NOT NULL DEFAULT 0 CHECK(deleted IN (0,1));
-- +migrate StatementEnd
