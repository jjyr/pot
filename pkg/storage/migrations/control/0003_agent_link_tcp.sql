-- +migrate Up
-- +migrate StatementBegin
DELETE FROM secrets WHERE id IN (SELECT secret_ref FROM agent_link_connections UNION SELECT secret_ref FROM agent_link_invitations);
DELETE FROM agent_link_invitations;
ALTER TABLE agent_link_connections RENAME COLUMN token_hash TO peer_key;
UPDATE agent_link_connections SET peer_key='',secret_ref='',state=CASE WHEN state='revoked' THEN 'revoked' ELSE 'offline' END,
metadata=json_set(metadata,'$.schema_version',2,'$.peer_host',json('{"id":"","public_key":""}'),'$.endpoint','','$.state',CASE WHEN state='revoked' THEN 'revoked' ELSE 'offline' END,'$.last_seen',json('null'),'$.last_error',CASE WHEN state='revoked' THEN '' ELSE 'agent_link_reconnect_required' END,'$.revocation_pending',json('false'));
-- +migrate StatementEnd
