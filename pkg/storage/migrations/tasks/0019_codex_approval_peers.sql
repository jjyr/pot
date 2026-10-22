-- +migrate Up
CREATE TABLE codex_approval_peers (
 assistant_id TEXT NOT NULL REFERENCES codex_link_assistants(id),
 node_id TEXT NOT NULL,
 PRIMARY KEY(assistant_id,node_id)
);
