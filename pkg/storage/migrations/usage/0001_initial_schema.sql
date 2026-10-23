-- +migrate Up
-- +migrate StatementBegin
-- First public schema baseline; pre-release databases are intentionally unsupported.
CREATE TABLE compaction_evidence(sequence INTEGER PRIMARY KEY AUTOINCREMENT,compaction_id TEXT NOT NULL REFERENCES compactions(id),at INTEGER NOT NULL,body TEXT NOT NULL);

CREATE TABLE compactions(id TEXT PRIMARY KEY,scope TEXT NOT NULL,turn_id TEXT NOT NULL,at INTEGER NOT NULL,end_at INTEGER,reason TEXT NOT NULL,state TEXT NOT NULL DEFAULT 'running',tokens_before INTEGER,tokens_after INTEGER,entry_id TEXT);

CREATE TABLE request_routes(request_id TEXT PRIMARY KEY REFERENCES requests(id),provider TEXT NOT NULL,generation INTEGER NOT NULL,spec_hash TEXT NOT NULL,agent TEXT NOT NULL,profile_id TEXT NOT NULL);

CREATE TABLE requests(id TEXT PRIMARY KEY,scope TEXT NOT NULL,turn_id TEXT NOT NULL,at INTEGER NOT NULL,model TEXT NOT NULL,input INTEGER,output INTEGER,cache INTEGER NOT NULL DEFAULT 0,reserved INTEGER NOT NULL DEFAULT 0,state TEXT NOT NULL DEFAULT 'pending',compaction_id TEXT,input_estimate INTEGER NOT NULL,output_cap INTEGER NOT NULL,original TEXT NOT NULL, cache_reported INTEGER NOT NULL DEFAULT 0);

CREATE TABLE routing_turns(turn_id TEXT PRIMARY KEY);

CREATE TABLE settings(key TEXT PRIMARY KEY,value TEXT NOT NULL);

CREATE TABLE tools(id TEXT PRIMARY KEY,scope TEXT NOT NULL,turn_id TEXT NOT NULL,name TEXT NOT NULL,at INTEGER NOT NULL,end_at INTEGER,bytes INTEGER,failed INTEGER NOT NULL DEFAULT 0);

CREATE TABLE wait_release_requests(owner TEXT NOT NULL,request_id TEXT NOT NULL,scope TEXT NOT NULL,turn_id TEXT NOT NULL,since INTEGER NOT NULL,complete INTEGER NOT NULL,PRIMARY KEY(owner,request_id));

CREATE TABLE wait_sequences(scope TEXT PRIMARY KEY,since INTEGER NOT NULL);

CREATE TABLE waits(scope TEXT PRIMARY KEY,turn_id TEXT NOT NULL,at INTEGER NOT NULL,reset_at INTEGER NOT NULL,required INTEGER NOT NULL,released INTEGER NOT NULL DEFAULT 0,provider TEXT NOT NULL DEFAULT '');

CREATE INDEX compaction_evidence_operation ON compaction_evidence(compaction_id,sequence);

CREATE INDEX compaction_scope ON compactions(scope,at);

CREATE INDEX request_compaction ON requests(compaction_id);

CREATE INDEX tools_scope ON tools(scope,at);

CREATE INDEX usage_scope ON requests(scope,at);

CREATE INDEX usage_time ON requests(at);

CREATE INDEX usage_turn ON requests(scope,turn_id,at);

-- +migrate StatementEnd
