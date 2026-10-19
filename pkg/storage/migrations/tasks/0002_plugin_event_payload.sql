-- +migrate Up
-- +migrate StatementBegin
DROP INDEX IF EXISTS agent_plugin_pending;
DROP TABLE IF EXISTS plugin_delivery_rates;
UPDATE agent_messages SET envelope=json_set(json_remove(envelope,'$.plugin.event.group'),'$.text','')
WHERE json_extract(envelope,'$.kind')='plugin_event' AND (json_type(envelope,'$.plugin.event.group') IS NOT NULL OR json_extract(envelope,'$.text')!='');
-- +migrate StatementEnd
