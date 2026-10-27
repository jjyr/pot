-- +migrate Up
-- +migrate StatementBegin
-- Transport receipts retain C0 dispatch/execution correlation, never launch work.
CREATE TABLE mcp_receiver_endpoints (
 connection_id TEXT NOT NULL, address TEXT NOT NULL, registration TEXT NOT NULL,
 PRIMARY KEY(connection_id,address), UNIQUE(address)
);
CREATE TABLE mcp_service_tasks (
 id TEXT PRIMARY KEY, connection_id TEXT NOT NULL, actor TEXT NOT NULL,
 request_id TEXT NOT NULL, digest TEXT NOT NULL, submission TEXT NOT NULL,
 task TEXT NOT NULL, current_claim TEXT NOT NULL DEFAULT '', execution TEXT NOT NULL DEFAULT '',
 cancel_requested INTEGER NOT NULL DEFAULT 0 CHECK(cancel_requested IN (0,1)),
 UNIQUE(connection_id,actor,request_id)
);
CREATE TABLE mcp_service_deliveries (
 id TEXT PRIMARY KEY, task_id TEXT NOT NULL REFERENCES mcp_service_tasks(id),
 receiver_connection TEXT NOT NULL, receiver_address TEXT NOT NULL,
 work TEXT NOT NULL DEFAULT '', control TEXT NOT NULL DEFAULT '',
 claim TEXT NOT NULL DEFAULT '', ack TEXT NOT NULL DEFAULT '', admission TEXT NOT NULL DEFAULT '',
 last_sequence INTEGER NOT NULL DEFAULT 0, terminal INTEGER NOT NULL DEFAULT 0,
 CHECK((work='') != (control=''))
);
CREATE INDEX mcp_service_delivery_receiver ON mcp_service_deliveries(receiver_connection,id);
CREATE TABLE mcp_service_mutations (
 connection_id TEXT NOT NULL, actor TEXT NOT NULL, operation TEXT NOT NULL,
 request_id TEXT NOT NULL, digest TEXT NOT NULL, receipt TEXT NOT NULL,
 PRIMARY KEY(connection_id,actor,operation,request_id)
);
CREATE TABLE mcp_service_events (
 claim_id TEXT NOT NULL, sequence INTEGER NOT NULL, digest TEXT NOT NULL,
 event TEXT NOT NULL, receipt TEXT NOT NULL,
 PRIMARY KEY(claim_id,sequence)
);
CREATE TABLE mcp_service_claim_requests (
 connection_id TEXT NOT NULL, request_id TEXT NOT NULL, receipt TEXT NOT NULL,
 PRIMARY KEY(connection_id,request_id)
);
-- +migrate StatementEnd
