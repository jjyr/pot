-- +migrate Up
-- +migrate StatementBegin
UPDATE metadata
SET value = json_insert(value, '$.intent.task_default.context_window', NULL, '$.intent.task_default.max_output_tokens', NULL, '$.intent.task_default.reasoning_effort', NULL)
WHERE key = 'assistant.configuration.v1' AND json_valid(value) AND json_type(value, '$.intent.task_default') = 'object';
UPDATE assistant_configuration_requests
SET response = json_insert(response, '$.intent.task_default.context_window', NULL, '$.intent.task_default.max_output_tokens', NULL, '$.intent.task_default.reasoning_effort', NULL)
WHERE json_valid(response) AND json_type(response, '$.intent.task_default') = 'object';
-- +migrate StatementEnd
