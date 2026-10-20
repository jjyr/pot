-- +migrate Up
-- +migrate StatementBegin
CREATE TABLE extension_settings(kind TEXT NOT NULL CHECK(kind IN ('plugins','skills')),id TEXT NOT NULL,enabled INTEGER NOT NULL CHECK(enabled IN (0,1)),revision INTEGER NOT NULL,source TEXT NOT NULL DEFAULT '',PRIMARY KEY(kind,id));
CREATE TABLE mcp_servers(id TEXT PRIMARY KEY,revision INTEGER NOT NULL,body TEXT NOT NULL);
-- +migrate StatementEnd
