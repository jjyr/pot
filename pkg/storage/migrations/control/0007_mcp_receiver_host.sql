-- +migrate Up
-- +migrate StatementBegin
CREATE TABLE mcp_receiver_connections (
 id TEXT PRIMARY KEY,url TEXT NOT NULL,node_id TEXT NOT NULL,secret_ref TEXT NOT NULL UNIQUE,
 state TEXT NOT NULL CHECK(state IN ('active','disconnected','revoked')),
 claim_request_id TEXT NOT NULL DEFAULT '',revision INTEGER NOT NULL DEFAULT 1 CHECK(revision>0)
);
-- JSON v1 in existing encrypted secrets: schema_version/access_token/token_type/
-- expires_at (RFC3339)/client_id. No historical credentials are imported.
-- +migrate StatementEnd
