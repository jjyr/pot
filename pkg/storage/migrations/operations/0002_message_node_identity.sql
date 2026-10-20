-- +migrate Up
-- +migrate StatementBegin
-- Only the built-in send_message tool has typed AgentRef arguments. Other tool
-- arguments/results remain opaque, even when JSON happens to resemble an address.
UPDATE operation_inputs SET body=pot_node_json(body,'operation') WHERE json_extract(body,'$.tool')='send_message';
UPDATE operations SET input_hash=(SELECT pot_node_hash(body) FROM operation_inputs WHERE operation_inputs.id=operations.id)
 WHERE EXISTS(SELECT 1 FROM operation_inputs WHERE operation_inputs.id=operations.id AND json_extract(body,'$.tool')='send_message');
UPDATE operations SET result=pot_node_json(CAST(result AS TEXT),'envelope')
 WHERE result IS NOT NULL AND json_valid(result) AND EXISTS(SELECT 1 FROM operation_inputs WHERE operation_inputs.id=operations.id AND json_extract(body,'$.tool')='send_message');
-- +migrate StatementEnd
