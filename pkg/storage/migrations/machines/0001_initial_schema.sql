-- +migrate Up
-- +migrate StatementBegin
-- First public schema baseline; pre-release databases are intentionally unsupported.
CREATE TABLE node_identity(id INTEGER PRIMARY KEY CHECK(id=1),body TEXT NOT NULL);

CREATE TABLE "retired_membership_state"(id INTEGER PRIMARY KEY CHECK(id=1),body TEXT NOT NULL);

-- +migrate StatementEnd
