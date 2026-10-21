-- +migrate Up
-- +migrate StatementBegin
ALTER TABLE requests ADD COLUMN cache_reported INTEGER NOT NULL DEFAULT 0;
UPDATE requests SET cache_reported=1 WHERE cache>0;
-- +migrate StatementEnd
