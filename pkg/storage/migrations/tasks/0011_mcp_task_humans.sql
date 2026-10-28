-- +migrate Up
-- +migrate StatementBegin
-- Human provenance is fixed with the original submission intent. Existing work
-- has no recoverable source human; do not infer authority for it during upgrade.
CREATE TABLE mcp_task_humans(
 connection_id TEXT NOT NULL,actor TEXT NOT NULL,request_id TEXT NOT NULL,
 human TEXT NOT NULL,PRIMARY KEY(connection_id,actor,request_id));
-- +migrate StatementEnd
