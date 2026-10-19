-- +migrate Up
CREATE TABLE agent_link_host (
  singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
  listen TEXT NOT NULL,
  endpoint TEXT NOT NULL DEFAULT '',
  mode TEXT NOT NULL DEFAULT 'https'
);
