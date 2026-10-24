-- +migrate Up
-- +migrate StatementBegin
ALTER TABLE codex_agent_host RENAME TO agent_adapter_host;
ALTER TABLE codex_agent_endpoints RENAME TO agent_adapter_endpoints;
ALTER TABLE agent_adapter_host ADD COLUMN adapter TEXT NOT NULL DEFAULT 'codex';
-- +migrate StatementEnd
