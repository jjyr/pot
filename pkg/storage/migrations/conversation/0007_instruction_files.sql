-- +migrate Up
-- +migrate StatementBegin
CREATE TABLE assistant_instruction_file_export(singleton INTEGER PRIMARY KEY CHECK(singleton=1),body TEXT NOT NULL);
INSERT INTO assistant_instruction_file_export SELECT 1,value FROM metadata WHERE key='assistant.instructions.v1';
DELETE FROM metadata WHERE key='assistant.instructions.v1';
DROP TABLE assistant_instruction_updates;
-- +migrate StatementEnd
