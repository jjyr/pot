-- +migrate Up
-- +migrate StatementBegin
-- Existing tasks retain their original destination, run and recovery receipts.
-- Explicit nulls describe the newly introduced collaboration metadata fields.
UPDATE tasks SET body=json_set(body,'$.request.link_id','','$.request.target',json('null'),'$.info.agent_link',json('null')) WHERE json_type(body,'$.request')='object' AND json_type(body,'$.info')='object';
UPDATE task_run_history SET body=json_set(body,'$.request.link_id','','$.request.target',json('null'),'$.info.agent_link',json('null')) WHERE json_type(body,'$.request')='object' AND json_type(body,'$.info')='object';
-- +migrate StatementEnd
