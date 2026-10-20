-- +migrate Up
-- +migrate StatementBegin
UPDATE agent_link_connections SET metadata=pot_node_json(metadata,'connection');
UPDATE agent_link_connections SET route=COALESCE(pot_node_route(metadata),route);
UPDATE agent_link_invitations SET identity=pot_node_json(identity,'identity');
ALTER TABLE codex_agent_host RENAME COLUMN control_id TO node_id;
-- Encrypted pair intents and invitation claim hashes must be converted with the
-- existing master key before any Agent Link service starts. The second stage
-- commits ciphertext, hashes and this receipt in one transaction.
CREATE TABLE node_identity_secret_migration(singleton INTEGER PRIMARY KEY CHECK(singleton=1));
INSERT INTO node_identity_secret_migration SELECT 1 WHERE EXISTS(SELECT 1 FROM agent_link_connections WHERE secret_ref!='' AND (state!='revoked' OR json_extract(metadata,'$.revocation_pending')=1)) OR EXISTS(SELECT 1 FROM agent_link_invitations WHERE claim_hash!='');
-- +migrate StatementEnd
