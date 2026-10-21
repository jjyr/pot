-- +migrate Up
-- Expand cached feature flags into per-assistant operation descriptions. Keep
-- every connection, peer pin, message, task receipt and accepted request intact.
UPDATE agent_link_connections
SET metadata=json_remove(json_set(metadata,'$.directory.operations',json((
 SELECT COALESCE(json_group_object(json_extract(assistant.value,'$.address.agent_id'),
  json_object('version',1,'scope','peer','operations',json((
   SELECT json_group_array(operation.value)
   FROM json_each(metadata,'$.directory.capabilities') AS capability
   JOIN json_each(CASE capability.value
    WHEN 'chat' THEN '["chat.read","chat.send","chat.receipt"]'
    WHEN 'tasks' THEN '["task.submit","task.read"]'
    WHEN 'cancel' THEN '["task.cancel"]'
    ELSE '[]' END) AS operation
  )))), '{}')
 FROM json_each(metadata,'$.directory.assistants') AS assistant
))), '$.directory.capabilities')
WHERE json_type(metadata,'$.directory')='object';
