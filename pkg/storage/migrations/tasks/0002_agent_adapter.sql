-- +migrate Up
-- +migrate StatementBegin
ALTER TABLE codex_link_assistants RENAME TO agent_adapter_assistants;
ALTER TABLE codex_link_jobs RENAME TO agent_adapter_jobs;
ALTER TABLE codex_task_executor RENAME TO agent_adapter_executor;
ALTER TABLE codex_task_sessions RENAME TO agent_adapter_sessions;
ALTER TABLE codex_task_inputs RENAME TO agent_adapter_inputs;
ALTER TABLE agent_adapter_assistants ADD COLUMN model TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_adapter_assistants ADD COLUMN effort TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_adapter_executor ADD COLUMN model TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_adapter_executor ADD COLUMN effort TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_adapter_sessions ADD COLUMN model TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_adapter_sessions ADD COLUMN effort TEXT NOT NULL DEFAULT '';
CREATE TABLE agent_adapter_management(caller TEXT NOT NULL, request_id TEXT NOT NULL, payload TEXT NOT NULL, result TEXT NOT NULL, PRIMARY KEY(caller,request_id));
-- +migrate StatementEnd
