-- +migrate Up
-- +migrate StatementBegin
CREATE TABLE IF NOT EXISTS requests(id TEXT PRIMARY KEY,scope TEXT NOT NULL,turn_id TEXT NOT NULL,at INTEGER NOT NULL,model TEXT NOT NULL,input INTEGER,output INTEGER,cache INTEGER NOT NULL DEFAULT 0,reserved INTEGER NOT NULL DEFAULT 0,state TEXT NOT NULL DEFAULT 'pending',compaction_id TEXT,input_estimate INTEGER NOT NULL,output_cap INTEGER NOT NULL,original TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS usage_time ON requests(at);
CREATE INDEX IF NOT EXISTS usage_scope ON requests(scope,at);
CREATE INDEX IF NOT EXISTS usage_turn ON requests(scope,turn_id,at);
CREATE TABLE IF NOT EXISTS request_routes(request_id TEXT PRIMARY KEY REFERENCES requests(id),provider TEXT NOT NULL,generation INTEGER NOT NULL,spec_hash TEXT NOT NULL,agent TEXT NOT NULL,profile_id TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS settings(key TEXT PRIMARY KEY,value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS waits(scope TEXT PRIMARY KEY,turn_id TEXT NOT NULL,at INTEGER NOT NULL,reset_at INTEGER NOT NULL,required INTEGER NOT NULL,released INTEGER NOT NULL DEFAULT 0,provider TEXT NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS wait_sequences(scope TEXT PRIMARY KEY,since INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS wait_release_requests(owner TEXT NOT NULL,request_id TEXT NOT NULL,scope TEXT NOT NULL,turn_id TEXT NOT NULL,since INTEGER NOT NULL,complete INTEGER NOT NULL,PRIMARY KEY(owner,request_id));
CREATE TABLE IF NOT EXISTS routing_turns(turn_id TEXT PRIMARY KEY);
CREATE TABLE IF NOT EXISTS tools(id TEXT PRIMARY KEY,scope TEXT NOT NULL,turn_id TEXT NOT NULL,name TEXT NOT NULL,at INTEGER NOT NULL,end_at INTEGER,bytes INTEGER,failed INTEGER NOT NULL DEFAULT 0);
CREATE INDEX IF NOT EXISTS tools_scope ON tools(scope,at);
CREATE TABLE IF NOT EXISTS compactions(id TEXT PRIMARY KEY,scope TEXT NOT NULL,turn_id TEXT NOT NULL,at INTEGER NOT NULL,end_at INTEGER,reason TEXT NOT NULL,state TEXT NOT NULL DEFAULT 'running',tokens_before INTEGER,tokens_after INTEGER,entry_id TEXT);
CREATE INDEX IF NOT EXISTS compaction_scope ON compactions(scope,at);
CREATE INDEX IF NOT EXISTS request_compaction ON requests(compaction_id);
-- +migrate StatementEnd
