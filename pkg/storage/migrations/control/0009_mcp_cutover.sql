-- +migrate Up
-- +migrate StatementBegin
-- Save only immutable presentation/history. Pairing keys, invitations, endpoints
-- and pending handshakes cannot become an MCP grant or executable connection.
CREATE TABLE agent_link_history(id TEXT PRIMARY KEY,metadata TEXT NOT NULL CHECK(json_valid(metadata)));
INSERT INTO agent_link_history(id,metadata)
SELECT id,json_object(
 'schema_version',1,'id',id,'local',json_extract(metadata,'$.local'),
 'peer',json_extract(metadata,'$.peer'),'endpoint',COALESCE(json_extract(metadata,'$.endpoint'),''),
 'state','unsupported','last_known_state',state,
 'created_at',json_extract(metadata,'$.created_at'),'updated_at',json_extract(metadata,'$.updated_at'),
 'last_seen',json_extract(metadata,'$.last_seen'),'directory',json_extract(metadata,'$.directory'))
FROM agent_link_connections;
DROP TABLE agent_link_claim_digest_formats;
DROP TABLE agent_link_invitations;
DROP TABLE agent_link_connections;
DROP TABLE agent_link_endpoints;
DROP TABLE agent_link_host;
DROP TABLE agent_adapter_endpoints;
ALTER TABLE agent_adapter_host DROP COLUMN endpoint;
ALTER TABLE agent_adapter_host DROP COLUMN peer_public_key;
ALTER TABLE agent_adapter_host DROP COLUMN peer_secret_ref;
-- There are no remaining P2P credentials to convert. Existing business records
-- were converted by 0004; do not read/decrypt obsolete pairing secrets at startup.
DROP TABLE node_identity_secret_migration;
-- +migrate StatementEnd
