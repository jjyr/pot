-- +migrate Up
-- +migrate StatementBegin
-- Resolve accepted receivers before replacing public selectors. Unknown default
-- recipients are fenced rather than being reassigned to a current default.
UPDATE tasks SET body=pot_target_record(body,(SELECT node_id FROM task_node WHERE singleton=1)) WHERE json_extract(body,'$.schema_version')=12;
UPDATE task_run_history SET body=pot_target_record(body,(SELECT node_id FROM task_node WHERE singleton=1)) WHERE json_extract(body,'$.schema_version')=12;
UPDATE task_writes AS w SET digest=(SELECT pot_target_digest(t.body) FROM tasks t WHERE t.assistant_id=w.assistant_id AND t.id=w.task_id)
WHERE EXISTS(SELECT 1 FROM tasks t WHERE t.assistant_id=w.assistant_id AND t.id=w.task_id AND json_extract(t.body,'$.schema_version')=13 AND json_extract(t.body,'$.request.request_id')=w.request_id AND json_type(t.body,'$.origin.kind')='text');
UPDATE codex_link_jobs SET data=pot_target_job(data) WHERE json_type(data,'$.request')='object' AND json_type(data,'$.task')='object';
UPDATE codex_link_jobs SET payload=pot_target_payload(data) WHERE json_valid(payload) AND json_type(data,'$.request.target')='object';
-- +migrate StatementEnd
