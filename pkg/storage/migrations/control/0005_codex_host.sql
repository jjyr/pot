-- +migrate Up
CREATE TABLE codex_agent_host(singleton INTEGER PRIMARY KEY CHECK(singleton=1),control_id TEXT NOT NULL,owner_secret_ref TEXT NOT NULL,endpoint TEXT NOT NULL DEFAULT '');
