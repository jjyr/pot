-- +migrate Up
-- +migrate StatementBegin
-- Change the accepted request and its deduplication digest atomically. Run,
-- delivery, cancellation and archive receipts retain their original identity.
UPDATE task_writes AS w SET digest=(
 SELECT pot_agent_task_digest(t.body) FROM tasks t
 WHERE t.assistant_id=w.assistant_id AND t.id=w.task_id
) WHERE EXISTS (
 SELECT 1 FROM tasks t WHERE t.assistant_id=w.assistant_id AND t.id=w.task_id
 AND json_extract(t.body,'$.request.executor')='codex'
);
UPDATE tasks SET body=json_set(body,'$.request.executor','agent')
WHERE json_extract(body,'$.request.executor')='codex';
UPDATE tasks SET body=json_set(body,'$.execution.request.executor','agent')
WHERE json_extract(body,'$.execution.request.executor')='codex';
UPDATE task_run_history SET body=json_set(body,'$.request.executor','agent')
WHERE json_extract(body,'$.request.executor')='codex';
UPDATE task_run_history SET body=json_set(body,'$.execution.request.executor','agent')
WHERE json_extract(body,'$.execution.request.executor')='codex';
-- Exact receiver retries must match Go's canonical TaskRequest encoding.
UPDATE codex_link_jobs SET payload=pot_agent_task_payload(payload)
WHERE CASE WHEN json_valid(payload) THEN json_extract(payload,'$.executor')='codex' ELSE 0 END;
UPDATE codex_link_jobs SET data=json_set(data,'$.request.executor','agent')
WHERE json_extract(data,'$.request.executor')='codex';
-- +migrate StatementEnd
