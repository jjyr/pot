-- +migrate Up
-- +migrate StatementBegin
ALTER TABLE mcp_receiver_connections ADD COLUMN remote_node_id TEXT NOT NULL DEFAULT '';
ALTER TABLE mcp_receiver_connections ADD COLUMN directory TEXT NOT NULL DEFAULT '';
ALTER TABLE mcp_receiver_connections ADD COLUMN last_seen_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE mcp_receiver_connections ADD COLUMN last_error TEXT NOT NULL DEFAULT '';
CREATE TABLE mcp_pot_authorizations (
 state TEXT PRIMARY KEY, url TEXT NOT NULL, node_id TEXT NOT NULL,
 client_id TEXT NOT NULL, redirect_uri TEXT NOT NULL,
 connection_id TEXT NOT NULL DEFAULT '', secret_ref TEXT NOT NULL,
 expires_at INTEGER NOT NULL
);
-- +migrate StatementEnd
