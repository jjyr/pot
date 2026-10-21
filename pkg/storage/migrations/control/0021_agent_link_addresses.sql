-- +migrate Up
CREATE TABLE agent_link_endpoints(endpoint TEXT PRIMARY KEY NOT NULL);
INSERT INTO agent_link_endpoints(endpoint) SELECT endpoint FROM agent_link_host WHERE endpoint<>'';
