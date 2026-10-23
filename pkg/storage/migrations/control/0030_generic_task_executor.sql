-- +migrate Up
-- +migrate StatementBegin
-- Archived submissions may still be awaiting their one-time Tasks import.
-- Opaque historical payload evidence is retained without decoding it.
UPDATE retired_task_submissions
SET payload=json_set(payload,'$.executor','agent')
WHERE CASE WHEN json_valid(payload) THEN json_extract(payload,'$.executor')='codex' ELSE 0 END;
UPDATE retired_task_submissions
SET body=json_set(body,'$.request.executor','agent')
WHERE json_extract(body,'$.request.executor')='codex';
-- +migrate StatementEnd
