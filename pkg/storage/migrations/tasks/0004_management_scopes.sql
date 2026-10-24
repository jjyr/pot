-- +migrate Up
-- +migrate StatementBegin
CREATE TABLE agent_adapter_workspaces(id TEXT PRIMARY KEY, workspace TEXT NOT NULL, mode TEXT NOT NULL CHECK(mode IN ('read-only','workspace-write')), UNIQUE(workspace,mode));
INSERT INTO agent_adapter_workspaces(id,workspace,mode) SELECT 'scope-' || lower(hex(randomblob(16))),workspace,mode FROM agent_adapter_assistants GROUP BY workspace,mode;
-- Stored management receipts use a private v1 representation. Translate scope
-- references while preserving their caller, request, payload and committed result.
UPDATE agent_adapter_management SET result=json_set(result,'$.assistant.workspace',COALESCE((SELECT w.id FROM agent_adapter_assistants a JOIN agent_adapter_workspaces w ON w.workspace=a.workspace AND w.mode=a.mode WHERE a.id=json_extract(result,'$.assistant.id')),json_extract(result,'$.assistant.workspace')),'$.assistant.avatar',COALESCE((SELECT a.avatar FROM agent_adapter_assistants a WHERE a.id=json_extract(result,'$.assistant.id')),'')) WHERE json_extract(result,'$.assistant') IS NOT NULL;
-- +migrate StatementEnd
