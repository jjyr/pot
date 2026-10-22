-- +migrate Up
-- Retain identities, accepted delegations and history. Refresh cached Codex
-- capabilities from the live node before exposing the task executor contract.
UPDATE agent_link_connections SET metadata=json_remove(metadata,'$.directory')
WHERE json_extract(metadata,'$.peer.adapter')='codex';
