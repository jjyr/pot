-- +migrate Up
-- +migrate StatementBegin
ALTER TABLE plugin_background ADD COLUMN plugin_id TEXT NOT NULL DEFAULT '';
ALTER TABLE plugin_background ADD COLUMN connection_id TEXT NOT NULL DEFAULT '';
UPDATE plugin_background SET plugin_id=id;
-- Existing instance IDs, event IDs, routing and delivery receipts stay intact.
UPDATE agent_messages SET envelope=json_set(envelope,'$.plugin.instance_id',json_extract(envelope,'$.plugin.identity.id'))
WHERE json_extract(envelope,'$.kind')='plugin_event';
CREATE INDEX agent_plugin_instance_state ON agent_messages(json_extract(envelope,'$.plugin.instance_id'),state);
-- +migrate StatementEnd
