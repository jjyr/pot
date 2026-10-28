-- +migrate Up
-- +migrate StatementBegin
UPDATE assistant_initial_intent
SET intent = json_insert(intent, '$.task_default', NULL)
WHERE json_valid(intent)
  AND json_type(intent) = 'object'
  AND json_type(intent, '$.task_default') IS NULL;
-- +migrate StatementEnd
