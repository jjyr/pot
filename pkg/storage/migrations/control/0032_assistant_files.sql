-- +migrate Up
-- +migrate StatementBegin
CREATE TABLE assistant_file_export(id TEXT PRIMARY KEY,name TEXT NOT NULL,avatar TEXT NOT NULL,character TEXT NOT NULL,seed TEXT NOT NULL,rules TEXT NOT NULL);
INSERT INTO assistant_file_export
SELECT d.id,d.name,COALESCE(p.avatar,''),COALESCE(NULLIF(p.character,''),pot_avatar_character(d.created_name,d.request_id)),d.request_id,COALESCE(i.rules,'')
FROM assistant_definitions d LEFT JOIN assistant_profiles p ON p.assistant_id=d.id LEFT JOIN assistant_initial i ON i.id=d.id;
ALTER TABLE assistant_definitions DROP COLUMN name;
ALTER TABLE assistant_definitions DROP COLUMN revision;
ALTER TABLE assistant_definitions DROP COLUMN created_name;
ALTER TABLE assistant_initial DROP COLUMN rules;
DROP TABLE assistant_profiles;
DROP TABLE assistant_profile_requests;
-- +migrate StatementEnd
