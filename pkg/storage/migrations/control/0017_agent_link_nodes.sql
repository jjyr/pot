-- +migrate Up
-- +migrate StatementBegin
-- Assistant-scoped links cannot silently acquire node-wide authority. Preserve
-- their identity/history, fence transport, and require a fresh node invitation.
UPDATE agent_link_connections SET state='offline',metadata=json_set(metadata,'$.state','offline','$.revocation_pending',json('false'),'$.last_error','agent_link_reconnect_required') WHERE state!='revoked';
UPDATE agent_link_invitations SET expires_at=0;
CREATE TABLE agent_link_node_settings(singleton INTEGER PRIMARY KEY CHECK(singleton=1),default_assistant_id TEXT NOT NULL);
CREATE TABLE agent_link_delegations(id TEXT PRIMARY KEY,link_id TEXT NOT NULL,owner TEXT NOT NULL,request_id TEXT NOT NULL,payload TEXT NOT NULL,remote_id TEXT NOT NULL DEFAULT '',body TEXT NOT NULL DEFAULT '{}',notified INTEGER NOT NULL DEFAULT 0,UNIQUE(owner,request_id));
CREATE TABLE agent_link_received_tasks(id TEXT PRIMARY KEY,caller TEXT NOT NULL,request_id TEXT NOT NULL,assistant_id TEXT NOT NULL,payload TEXT NOT NULL,task_id TEXT NOT NULL DEFAULT '',UNIQUE(caller,request_id));
CREATE TABLE agent_link_chat(id TEXT PRIMARY KEY,link_id TEXT NOT NULL,assistant_id TEXT NOT NULL,request_id TEXT NOT NULL,role TEXT NOT NULL,text TEXT NOT NULL,created_at INTEGER NOT NULL,UNIQUE(link_id,assistant_id,request_id,role));
-- +migrate StatementEnd
