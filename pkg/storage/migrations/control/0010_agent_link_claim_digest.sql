-- +migrate Up
-- +migrate StatementBegin
-- A claimed invitation binds the exact original Pair request, including its
-- names. The receiver stores its current registry identity, so the request's
-- original name cannot always be reconstructed. Keep that irreversible binding
-- under an explicit frozen hash format, independently of the new wire fields.
CREATE TABLE agent_link_claim_digest_formats(invitation TEXT PRIMARY KEY REFERENCES agent_link_invitations(id) ON DELETE CASCADE,format INTEGER NOT NULL CHECK(format=1));
INSERT INTO agent_link_claim_digest_formats SELECT id,1 FROM agent_link_invitations
 WHERE claim_hash!='' AND EXISTS(SELECT 1 FROM node_identity_secret_migration);
-- +migrate StatementEnd
