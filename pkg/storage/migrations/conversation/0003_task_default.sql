-- +migrate Up
-- +migrate StatementBegin
UPDATE metadata
SET value = json_insert(value, '$.intent.task_default', NULL)
WHERE key = 'assistant.configuration.v1'
  AND json_valid(value)
  AND json_type(value) = 'object'
  AND json_type(value, '$.intent') = 'object'
  AND json_type(value, '$.intent.task_default') IS NULL;

UPDATE assistant_configuration_requests
SET response = json_insert(response, '$.intent.task_default', NULL)
WHERE json_valid(response)
  AND json_type(response) = 'object'
  AND json_type(response, '$.intent') = 'object'
  AND json_type(response, '$.intent.task_default') IS NULL;
-- +migrate StatementEnd
