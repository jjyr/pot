-- +migrate Up
-- +migrate StatementBegin
-- Only current/pending settings adopt the new 600-second inactivity policy.
-- Keep historical receipts, model context, uncertain effects and run identities.
UPDATE metadata SET value=json_set(value,'$.config',json(pot_idle_model_spec(json_extract(value,'$.config'))))
WHERE key='thread' AND json_type(value,'$.config')='object';
UPDATE metadata SET value=json_set(value,'$.pending_spec',json(pot_idle_model_spec(json_extract(value,'$.pending_spec'))))
WHERE key='assistant.configuration.v1' AND json_type(value,'$.pending_spec')='object';
UPDATE metadata SET value=(SELECT json_group_array(json(pot_idle_model_spec(item.value))) FROM json_each(metadata.value) AS item)
WHERE key='agent.profiles.v1' AND json_type(value)='array';
-- +migrate StatementEnd
