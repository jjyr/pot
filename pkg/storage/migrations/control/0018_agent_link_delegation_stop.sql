-- +migrate Up
ALTER TABLE agent_link_delegations ADD COLUMN stop_requested INTEGER NOT NULL DEFAULT 0;
ALTER TABLE agent_link_chat ADD COLUMN sender TEXT NOT NULL DEFAULT '';
