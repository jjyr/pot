-- +migrate Up
-- +migrate StatementBegin
-- Local backend effects cross the Control/Tasks pools. A durable pending intent
-- permits read-only reconciliation after response loss, never automatic replay.
CREATE TABLE mcp_local_mutations (
 connection_id TEXT NOT NULL, actor TEXT NOT NULL, operation TEXT NOT NULL,
 request_id TEXT NOT NULL, digest TEXT NOT NULL, input TEXT NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('pending','accepted','rejected')),
 receipt TEXT NOT NULL DEFAULT '',
 PRIMARY KEY(connection_id,actor,operation,request_id)
);
-- +migrate StatementEnd
