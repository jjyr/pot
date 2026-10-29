-- +migrate Up
-- +migrate StatementBegin
UPDATE assistant_initial_intent
SET intent = json_insert(intent, '$.task_default.context_window', NULL, '$.task_default.max_output_tokens', NULL, '$.task_default.reasoning_effort', NULL)
WHERE json_valid(intent) AND json_type(intent, '$.task_default') = 'object';
-- +migrate StatementEnd
