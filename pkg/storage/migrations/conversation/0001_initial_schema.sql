-- +migrate Up
-- +migrate StatementBegin
-- First public schema baseline; pre-release databases are intentionally unsupported.
CREATE TABLE assistant_configuration_requests(id TEXT PRIMARY KEY,input_hash TEXT NOT NULL,response TEXT NOT NULL);

CREATE TABLE assistant_idle_compaction(singleton INTEGER PRIMARY KEY CHECK(singleton=1),request_id TEXT NOT NULL DEFAULT '',finished_at INTEGER NOT NULL DEFAULT 0,route TEXT NOT NULL DEFAULT '',attempt_for TEXT NOT NULL DEFAULT '',prefix TEXT NOT NULL DEFAULT '{}', started_at INTEGER NOT NULL DEFAULT 0, cache_read INTEGER, cache_write INTEGER);

CREATE TABLE assistant_instruction_file_export(singleton INTEGER PRIMARY KEY CHECK(singleton=1),body TEXT NOT NULL);

CREATE TABLE assistant_model_attempts(id TEXT PRIMARY KEY,owner TEXT NOT NULL,generation INTEGER NOT NULL,turn_id TEXT NOT NULL,at INTEGER NOT NULL);

CREATE TABLE assistant_observations(sequence INTEGER PRIMARY KEY AUTOINCREMENT,turn_id TEXT NOT NULL,generation INTEGER NOT NULL,body TEXT NOT NULL);

CREATE TABLE assistant_steering(id TEXT PRIMARY KEY,turn_id TEXT NOT NULL,text TEXT NOT NULL,at INTEGER NOT NULL,acknowledged INTEGER NOT NULL DEFAULT 0);

CREATE TABLE chat_user_inbox(sequence INTEGER PRIMARY KEY AUTOINCREMENT,request TEXT NOT NULL,message TEXT NOT NULL UNIQUE);

CREATE TABLE context_history(sequence INTEGER PRIMARY KEY AUTOINCREMENT,classification TEXT NOT NULL,body TEXT NOT NULL,at INTEGER NOT NULL);

CREATE TABLE events(cursor INTEGER PRIMARY KEY AUTOINCREMENT,body TEXT NOT NULL);

CREATE TABLE mail(scope TEXT NOT NULL,id TEXT NOT NULL,body TEXT NOT NULL,state TEXT NOT NULL,sequence INTEGER PRIMARY KEY AUTOINCREMENT,UNIQUE(scope,id));

CREATE TABLE main_message_outbox(sequence INTEGER PRIMARY KEY AUTOINCREMENT,message_id TEXT UNIQUE NOT NULL,body TEXT NOT NULL);

CREATE TABLE messages(id TEXT PRIMARY KEY,request_id TEXT UNIQUE,body TEXT NOT NULL);

CREATE TABLE metadata(key TEXT PRIMARY KEY,value TEXT NOT NULL);

INSERT INTO "assistant_idle_compaction" VALUES (1,'',0,'','','{}',0,NULL,NULL);
CREATE INDEX assistant_model_turn ON assistant_model_attempts(turn_id);

CREATE INDEX ui_resource_identity ON messages(json_extract(body,'$.presentation.resource.id'))
 WHERE json_extract(body,'$.presentation.resource.id') IS NOT NULL;

CREATE INDEX ui_tool_identity ON messages(json_extract(body,'$.tool.turn_id'),json_extract(body,'$.tool.id')) WHERE json_extract(body,'$.role')='tool';

-- +migrate StatementEnd
