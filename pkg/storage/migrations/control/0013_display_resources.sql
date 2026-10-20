-- +migrate Up
-- +migrate StatementBegin
CREATE TABLE display_resources(
 id TEXT PRIMARY KEY,
 owner TEXT NOT NULL,
 request_id TEXT NOT NULL UNIQUE,
 input_hash TEXT NOT NULL,
 metadata TEXT NOT NULL CHECK(json_valid(metadata)),
 size INTEGER NOT NULL CHECK(size>=0),
 created_at_ms INTEGER NOT NULL
);
CREATE INDEX display_resources_owner ON display_resources(owner);
-- +migrate StatementEnd
