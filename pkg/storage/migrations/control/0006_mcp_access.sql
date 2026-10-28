-- +migrate Up
-- +migrate StatementBegin
CREATE TABLE mcp_oauth_clients (
 id TEXT PRIMARY KEY, metadata TEXT NOT NULL
);
CREATE TABLE mcp_oauth_connections (
 id TEXT PRIMARY KEY, client_id TEXT NOT NULL REFERENCES mcp_oauth_clients(id),
 actor TEXT NOT NULL, receiver_node TEXT NOT NULL DEFAULT '',
 created_at INTEGER NOT NULL, revoked_at INTEGER NOT NULL DEFAULT 0
);
-- Only SHA-256 digests of random credentials are persisted.
CREATE TABLE mcp_oauth_codes (
 digest TEXT PRIMARY KEY, connection_id TEXT NOT NULL REFERENCES mcp_oauth_connections(id),
 client_id TEXT NOT NULL, redirect_uri TEXT NOT NULL, resource TEXT NOT NULL,
 challenge TEXT NOT NULL, expires_at INTEGER NOT NULL, consumed INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE mcp_oauth_tokens (
 digest TEXT PRIMARY KEY, connection_id TEXT NOT NULL REFERENCES mcp_oauth_connections(id),
 resource TEXT NOT NULL, expires_at INTEGER NOT NULL
);
CREATE TABLE mcp_oauth_pending (
 id TEXT PRIMARY KEY, client_id TEXT NOT NULL, redirect_uri TEXT NOT NULL,
 resource TEXT NOT NULL, challenge TEXT NOT NULL, state TEXT NOT NULL,
 session_digest TEXT NOT NULL, expires_at INTEGER NOT NULL
);
-- +migrate StatementEnd
