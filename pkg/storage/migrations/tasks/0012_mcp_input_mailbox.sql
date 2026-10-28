-- +migrate Up
-- +migrate StatementBegin
-- Completion envelopes retain actual receiver run identifiers (including native
-- hash IDs) in result.run_id and conversation_id; existing string IDs stay intact.
-- Mailbox disposition is separate from durable delivery ACK and run results.
ALTER TABLE mcp_service_deliveries ADD COLUMN input_receipt TEXT NOT NULL DEFAULT '';

-- Capture the phase at the original accepted input, not a later live run.
UPDATE mcp_service_deliveries AS d
SET control=json_set(control,'$.input_phase',COALESCE((
 SELECT json_extract(m.receipt,'$.task.state')
 FROM mcp_service_mutations m JOIN mcp_service_tasks t ON t.id=d.task_id
 WHERE m.connection_id=t.connection_id AND m.actor=t.actor
 AND m.operation='task.input'
 AND m.request_id=json_extract(d.control,'$.input.request_id')
),'unknown'))
WHERE control!='' AND json_type(control,'$.input')='object';

-- Lost-claim retries must return the same converted immutable delivery.
UPDATE mcp_service_claim_requests AS r
SET receipt=json_set(receipt,'$.control',json((
 SELECT d.control FROM mcp_service_deliveries d
 WHERE d.id=json_extract(r.receipt,'$.claim.delivery_id')
)))
WHERE json_type(receipt,'$.control.input')='object';

-- Existing admissions already selected their input exactly once. Do not replay.
UPDATE mcp_service_deliveries
SET input_receipt=json_object('claim',json(claim),
 'binding',json(json_extract(admission,'$.binding')),'state','consumed')
WHERE control!='' AND json_type(control,'$.input')='object' AND admission!='';

UPDATE mcp_receiver_journal AS r
SET body=json_set(body,'$.control.input_phase',COALESCE((
 SELECT json_extract(p.body,'$.last_phase') FROM mcp_receiver_journal p
 WHERE p.connection_id=r.connection_id
 AND p.execution_task=json_extract(r.body,'$.control.binding.task.agent_id')
 AND p.execution_run=json_extract(r.body,'$.control.binding.run_id')
),'unknown'))
WHERE json_type(body,'$.control.input')='object';

UPDATE mcp_receiver_journal
SET body=json_set(body,'$.input_receipt',json_object(
 'claim',json(json_extract(body,'$.claim')),
 'binding',json(json_extract(body,'$.admission.binding')),'state','consumed'))
WHERE json_type(body,'$.control.input')='object'
AND json_type(body,'$.admission')='object';
-- +migrate StatementEnd
