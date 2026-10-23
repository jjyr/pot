-- +migrate Up
-- +migrate StatementBegin
-- Preserve original bytes solely for historical reconciliation. New operations
-- use operations.sqlite and never create a second destination command receipt.
CREATE TABLE retired_execution_receipts AS
 SELECT c.control_id,c.task_id,c.run_id,c.command_id,c.digest,c.phase,c.reply,i.body AS input
 FROM run_commands c LEFT JOIN run_command_inputs i
 USING(control_id,task_id,run_id,command_id);
UPDATE task_runs SET phase='recovery_required',
 body=json_set(body,'$.phase','recovery_required')
 WHERE phase NOT IN ('complete','failed','cancelled','interrupted')
 AND EXISTS(SELECT 1 FROM retired_execution_receipts r WHERE r.control_id=task_runs.control_id AND r.task_id=task_runs.task_id AND r.run_id=task_runs.run_id);
DROP TABLE run_command_inputs;
DROP TABLE run_commands;
-- +migrate StatementEnd
