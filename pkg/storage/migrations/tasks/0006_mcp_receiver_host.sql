-- +migrate Up
-- +migrate StatementBegin
CREATE TABLE mcp_receiver_journal (
 connection_id TEXT NOT NULL, delivery_id TEXT NOT NULL, claim_id TEXT NOT NULL,
 revision INTEGER NOT NULL DEFAULT 1 CHECK(revision>0),
 body TEXT NOT NULL CHECK(json_valid(body)), execution_task TEXT, execution_run TEXT,
 PRIMARY KEY(connection_id,delivery_id), UNIQUE(connection_id,claim_id),
 UNIQUE(connection_id,execution_task,execution_run),
 CHECK((execution_task IS NULL)=(execution_run IS NULL))
);
-- +migrate StatementEnd
