-- +migrate Up
-- +migrate StatementBegin
-- Fence old native work without rewriting its last confirmed state or messages.
CREATE TABLE retired_agent_link_work(kind TEXT NOT NULL CHECK(kind IN ('job','message')),id TEXT NOT NULL,PRIMARY KEY(kind,id));
INSERT INTO retired_agent_link_work(kind,id)
 SELECT 'job',j.id FROM agent_adapter_jobs j WHERE NOT EXISTS(SELECT 1 FROM mcp_receiver_journal r WHERE r.execution_task=j.id);
INSERT INTO retired_agent_link_work(kind,id)
 SELECT 'message',m.id FROM agent_messages m WHERE NOT EXISTS(SELECT 1 FROM mcp_receiver_journal r WHERE json_extract(r.body,'$.work.message.id')=m.id)
 AND NOT EXISTS(SELECT 1 FROM mcp_local_mutations x WHERE x.receipt!='' AND json_extract(x.receipt,'$.message.message.id')=m.id)
 AND NOT EXISTS(SELECT 1 FROM mcp_service_deliveries d WHERE d.work!='' AND json_extract(d.work,'$.message.id')=m.id);

CREATE TABLE mcp_native_user_operations(
 id TEXT PRIMARY KEY,connection_id TEXT NOT NULL,address TEXT NOT NULL,epoch TEXT NOT NULL,
 request TEXT NOT NULL CHECK(json_valid(request)),state TEXT NOT NULL CHECK(state IN ('pending','claimed','finished','unknown')),
 result TEXT NOT NULL DEFAULT '',created_at INTEGER NOT NULL);
CREATE TABLE mcp_native_user_journal(
 id TEXT PRIMARY KEY,connection_id TEXT NOT NULL,request TEXT NOT NULL CHECK(json_valid(request)),
 state TEXT NOT NULL CHECK(state IN ('executing','finished','unknown')),result TEXT NOT NULL DEFAULT '');
CREATE TABLE mcp_native_user_profiles(connection_id TEXT NOT NULL,address TEXT NOT NULL,
 profile TEXT NOT NULL DEFAULT '',deleted INTEGER NOT NULL DEFAULT 0 CHECK(deleted IN (0,1)),PRIMARY KEY(connection_id,address));
-- +migrate StatementEnd
