-- +migrate Up
-- +migrate StatementBegin
-- First public schema baseline; pre-release databases are intentionally unsupported.
CREATE TABLE agent_link_chat(id TEXT PRIMARY KEY,link_id TEXT NOT NULL,assistant_id TEXT NOT NULL,request_id TEXT NOT NULL,role TEXT NOT NULL,text TEXT NOT NULL,created_at INTEGER NOT NULL, sender TEXT NOT NULL DEFAULT '', sender_kind TEXT NOT NULL DEFAULT 'assistant', reply_to TEXT NOT NULL DEFAULT '', reply_state TEXT NOT NULL DEFAULT 'pending', reply_error TEXT NOT NULL DEFAULT '', reply_uncertain INTEGER NOT NULL DEFAULT 0, checked_at INTEGER NOT NULL DEFAULT 0,UNIQUE(link_id,assistant_id,request_id,role));

CREATE TABLE agent_link_chat_sessions(link_id TEXT NOT NULL,assistant_id TEXT NOT NULL,id TEXT NOT NULL UNIQUE, revision INTEGER NOT NULL DEFAULT 1,PRIMARY KEY(link_id,assistant_id));

CREATE TABLE agent_link_claim_digest_formats(invitation TEXT PRIMARY KEY REFERENCES agent_link_invitations(id) ON DELETE CASCADE,format INTEGER NOT NULL CHECK(format=1));

CREATE TABLE agent_link_connections(id TEXT PRIMARY KEY,route TEXT NOT NULL,state TEXT NOT NULL,metadata TEXT NOT NULL,secret_ref TEXT NOT NULL,peer_key TEXT NOT NULL);

CREATE TABLE agent_link_endpoints(endpoint TEXT PRIMARY KEY NOT NULL);

CREATE TABLE agent_link_host (
  singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
  listen TEXT NOT NULL,
  endpoint TEXT NOT NULL DEFAULT '',
  mode TEXT NOT NULL DEFAULT 'https'
);

CREATE TABLE agent_link_invitations(id TEXT PRIMARY KEY,identity TEXT NOT NULL,endpoint TEXT NOT NULL,expires_at INTEGER NOT NULL,secret_ref TEXT NOT NULL,claim_hash TEXT NOT NULL DEFAULT '',connection_id TEXT NOT NULL DEFAULT '');

CREATE TABLE agent_link_node_settings(singleton INTEGER PRIMARY KEY CHECK(singleton=1),default_assistant_id TEXT NOT NULL);

CREATE TABLE agent_link_received_tasks(id TEXT PRIMARY KEY,caller TEXT NOT NULL,request_id TEXT NOT NULL,assistant_id TEXT NOT NULL,payload TEXT NOT NULL,task_id TEXT NOT NULL DEFAULT '',UNIQUE(caller,request_id));

CREATE TABLE assistant_definitions(sequence INTEGER PRIMARY KEY AUTOINCREMENT,id TEXT NOT NULL UNIQUE,request_id TEXT NOT NULL UNIQUE,deleted INTEGER NOT NULL DEFAULT 0 CHECK(deleted IN (0,1)));

CREATE TABLE assistant_file_export(id TEXT PRIMARY KEY,name TEXT NOT NULL,avatar TEXT NOT NULL,character TEXT NOT NULL,seed TEXT NOT NULL,rules TEXT NOT NULL);

CREATE TABLE assistant_initial(id TEXT PRIMARY KEY, binding TEXT NOT NULL, spec TEXT NOT NULL);

CREATE TABLE assistant_initial_intent(id TEXT PRIMARY KEY,intent TEXT NOT NULL);

CREATE TABLE assistant_registry_identity(singleton INTEGER PRIMARY KEY CHECK(singleton=1),root TEXT NOT NULL);

CREATE TABLE assistant_welcome(id TEXT PRIMARY KEY,text TEXT NOT NULL);

CREATE TABLE codex_agent_endpoints(endpoint TEXT PRIMARY KEY NOT NULL);

CREATE TABLE codex_agent_host(singleton INTEGER PRIMARY KEY CHECK(singleton=1),node_id TEXT NOT NULL,owner_secret_ref TEXT NOT NULL,endpoint TEXT NOT NULL DEFAULT '', peer_public_key TEXT NOT NULL DEFAULT '', peer_secret_ref TEXT NOT NULL DEFAULT '');

CREATE TABLE display_resources(
 id TEXT PRIMARY KEY,
 owner TEXT NOT NULL,
 request_id TEXT NOT NULL UNIQUE,
 input_hash TEXT NOT NULL,
 metadata TEXT NOT NULL CHECK(json_valid(metadata)),
 size INTEGER NOT NULL CHECK(size>=0),
 created_at_ms INTEGER NOT NULL
);

CREATE TABLE extension_file_export(singleton INTEGER PRIMARY KEY CHECK(singleton=1));

CREATE TABLE integration_accounts(service TEXT PRIMARY KEY, revision INTEGER NOT NULL, metadata TEXT, secret_ref TEXT NOT NULL);

CREATE TABLE node_identity_secret_migration(singleton INTEGER PRIMARY KEY CHECK(singleton=1));

CREATE TABLE provider_metadata(
 route_id TEXT NOT NULL REFERENCES providers(id),
 method TEXT NOT NULL CHECK(method IN ('list_models','read_allowances')),
 source_digest TEXT NOT NULL,
 payload TEXT NOT NULL CHECK(json_valid(payload)),
 PRIMARY KEY(route_id,method)
);

CREATE TABLE providers(id TEXT PRIMARY KEY,metadata TEXT NOT NULL);

CREATE TABLE "retired_extension_settings"(kind TEXT NOT NULL CHECK(kind IN ('plugins','skills')),id TEXT NOT NULL,enabled INTEGER NOT NULL CHECK(enabled IN (0,1)),revision INTEGER NOT NULL,source TEXT NOT NULL DEFAULT '',PRIMARY KEY(kind,id));

CREATE TABLE "retired_mcp_servers"(id TEXT PRIMARY KEY,revision INTEGER NOT NULL,body TEXT NOT NULL);

CREATE TABLE "retired_task_submissions"(id TEXT PRIMARY KEY,link_id TEXT NOT NULL,owner TEXT NOT NULL,request_id TEXT NOT NULL,payload TEXT NOT NULL,remote_id TEXT NOT NULL DEFAULT '',body TEXT NOT NULL DEFAULT '{}',notified INTEGER NOT NULL DEFAULT 0, stop_requested INTEGER NOT NULL DEFAULT 0,UNIQUE(owner,request_id));

CREATE TABLE secrets(id TEXT PRIMARY KEY,nonce BLOB NOT NULL,ciphertext BLOB NOT NULL,tag BLOB NOT NULL);

INSERT INTO "extension_file_export" VALUES (1);
CREATE UNIQUE INDEX agent_link_active_route ON agent_link_connections(route) WHERE state!='revoked';

CREATE INDEX agent_link_chat_replies ON agent_link_chat(reply_to);

CREATE INDEX display_resources_owner ON display_resources(owner);

-- +migrate StatementEnd
