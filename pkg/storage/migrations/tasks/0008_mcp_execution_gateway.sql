-- +migrate Up
-- +migrate StatementBegin
-- Optional message/approval deliveries retain old work and never launch it.
UPDATE mcp_receiver_journal SET body=json_set(body,'$.schema_version',2);
CREATE TABLE mcp_remote_approvals (
 connection_id TEXT NOT NULL, approval_id TEXT NOT NULL,
 task_id TEXT NOT NULL REFERENCES mcp_service_tasks(id), run_id TEXT NOT NULL,
 epoch TEXT NOT NULL, caller TEXT NOT NULL, body TEXT NOT NULL CHECK(json_valid(body)),
 state TEXT NOT NULL CHECK(state IN ('pending','decided','invalidated')),
 decision TEXT NOT NULL DEFAULT '',
 PRIMARY KEY(connection_id,approval_id)
);
-- +migrate StatementEnd
