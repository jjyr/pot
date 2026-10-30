-- +migrate Up
-- +migrate StatementBegin
ALTER TABLE context_rollout ADD COLUMN activity_at INTEGER NOT NULL DEFAULT 0;
-- +migrate StatementEnd
