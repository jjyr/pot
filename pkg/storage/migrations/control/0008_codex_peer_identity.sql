-- +migrate Up
-- +migrate StatementBegin
ALTER TABLE codex_agent_host ADD COLUMN peer_public_key TEXT NOT NULL DEFAULT '';
ALTER TABLE codex_agent_host ADD COLUMN peer_secret_ref TEXT NOT NULL DEFAULT '';
UPDATE codex_agent_host SET endpoint='';
-- +migrate StatementEnd
