-- +migrate Up
-- +migrate StatementBegin
ALTER TABLE extension_settings RENAME TO retired_extension_settings;
ALTER TABLE mcp_servers RENAME TO retired_mcp_servers;
CREATE TABLE extension_file_export(singleton INTEGER PRIMARY KEY CHECK(singleton=1));
INSERT INTO extension_file_export VALUES(1);
-- +migrate StatementEnd
