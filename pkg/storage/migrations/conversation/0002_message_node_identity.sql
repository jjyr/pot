-- +migrate Up
-- Historical messages, events and context-history snapshots remain facts. Only
-- the current pending call can be executed again under the new address protocol.
UPDATE metadata SET value=pot_node_json(value,'checkpoint') WHERE key='thread.context';
