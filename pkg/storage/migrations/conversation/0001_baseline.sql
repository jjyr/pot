-- +migrate Up
-- +migrate StatementBegin
CREATE TABLE IF NOT EXISTS messages(id TEXT PRIMARY KEY,request_id TEXT UNIQUE,body TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS ui_tool_identity ON messages(json_extract(body,'$.tool.turn_id'),json_extract(body,'$.tool.id')) WHERE json_extract(body,'$.role')='tool';
CREATE TABLE IF NOT EXISTS events(cursor INTEGER PRIMARY KEY AUTOINCREMENT,body TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS metadata(key TEXT PRIMARY KEY,value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS main_message_outbox(sequence INTEGER PRIMARY KEY AUTOINCREMENT,message_id TEXT UNIQUE NOT NULL,body TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS context_history(sequence INTEGER PRIMARY KEY AUTOINCREMENT,classification TEXT NOT NULL,body TEXT NOT NULL,at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS mail(scope TEXT NOT NULL,id TEXT NOT NULL,body TEXT NOT NULL,state TEXT NOT NULL,sequence INTEGER PRIMARY KEY AUTOINCREMENT,UNIQUE(scope,id));
CREATE TABLE IF NOT EXISTS assistant_configuration_requests(id TEXT PRIMARY KEY,input_hash TEXT NOT NULL,response TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS assistant_model_attempts(id TEXT PRIMARY KEY,owner TEXT NOT NULL,generation INTEGER NOT NULL,turn_id TEXT NOT NULL,at INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS assistant_model_turn ON assistant_model_attempts(turn_id);
CREATE TABLE IF NOT EXISTS chat_user_inbox(sequence INTEGER PRIMARY KEY AUTOINCREMENT,request TEXT NOT NULL,message TEXT NOT NULL UNIQUE);
CREATE TABLE IF NOT EXISTS assistant_steering(id TEXT PRIMARY KEY,turn_id TEXT NOT NULL,text TEXT NOT NULL,at INTEGER NOT NULL,acknowledged INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS assistant_observations(sequence INTEGER PRIMARY KEY AUTOINCREMENT,turn_id TEXT NOT NULL,generation INTEGER NOT NULL,body TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS assistant_instruction_updates(id TEXT PRIMARY KEY,text TEXT NOT NULL,before_rules TEXT NOT NULL,result TEXT);
PRAGMA user_version=0;
-- +migrate StatementEnd
