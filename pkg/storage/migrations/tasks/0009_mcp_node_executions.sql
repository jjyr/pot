-- +migrate Up
-- +migrate StatementBegin
-- An authenticated node attests its actual local active run. This association
-- cannot enqueue, launch, replay work, or grant user/owner/approval authority.
CREATE TABLE mcp_node_executions (
 connection_id TEXT NOT NULL, task TEXT NOT NULL,
 execution_id TEXT NOT NULL, binding TEXT NOT NULL CHECK(json_valid(binding)),
 parent_id TEXT NOT NULL DEFAULT '', active INTEGER NOT NULL CHECK(active IN (0,1)),
 PRIMARY KEY(connection_id,task), UNIQUE(connection_id,execution_id)
);
-- +migrate StatementEnd
