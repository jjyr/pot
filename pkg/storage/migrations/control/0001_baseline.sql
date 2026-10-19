-- +migrate Up
-- +migrate StatementBegin
CREATE TABLE IF NOT EXISTS assistant_registry_identity(singleton INTEGER PRIMARY KEY CHECK(singleton=1),root TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS assistant_definitions(sequence INTEGER PRIMARY KEY AUTOINCREMENT,id TEXT NOT NULL UNIQUE,name TEXT NOT NULL,revision INTEGER NOT NULL,request_id TEXT NOT NULL UNIQUE,created_name TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS assistant_initial(id TEXT PRIMARY KEY, binding TEXT NOT NULL, spec TEXT NOT NULL,rules TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS assistant_initial_intent(id TEXT PRIMARY KEY,intent TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS assistant_welcome(id TEXT PRIMARY KEY,text TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS assistant_profiles(assistant_id TEXT PRIMARY KEY,avatar TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS assistant_profile_requests(id TEXT PRIMARY KEY,input_hash TEXT NOT NULL,response TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS providers(id TEXT PRIMARY KEY,metadata TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS secrets(id TEXT PRIMARY KEY,nonce BLOB NOT NULL,ciphertext BLOB NOT NULL,tag BLOB NOT NULL);
CREATE TABLE IF NOT EXISTS integration_accounts(service TEXT PRIMARY KEY, revision INTEGER NOT NULL, metadata TEXT, secret_ref TEXT NOT NULL);
-- +migrate StatementEnd
