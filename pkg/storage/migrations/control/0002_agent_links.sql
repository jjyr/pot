-- +migrate Up
-- +migrate StatementBegin
CREATE TABLE agent_link_connections(id TEXT PRIMARY KEY,route TEXT NOT NULL,state TEXT NOT NULL,metadata TEXT NOT NULL,secret_ref TEXT NOT NULL,token_hash TEXT NOT NULL);
CREATE UNIQUE INDEX agent_link_active_route ON agent_link_connections(route) WHERE state!='revoked';
CREATE TABLE agent_link_invitations(id TEXT PRIMARY KEY,identity TEXT NOT NULL,endpoint TEXT NOT NULL,expires_at INTEGER NOT NULL,secret_ref TEXT NOT NULL,claim_hash TEXT NOT NULL DEFAULT '',connection_id TEXT NOT NULL DEFAULT '');
-- +migrate StatementEnd
