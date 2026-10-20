-- +migrate Up
-- Preserve membership identity/epoch/credentials and pending join intent. The
-- old HTTP port belongs to owner management; it cannot be guessed as peer TCP.
UPDATE controlplane_state
SET body=json_remove(
  json_set(body, '$.version', 2),
  '$.membership.ca_pem'
);
UPDATE controlplane_state
SET body=json_set(body, '$.membership.origin', '')
WHERE json_type(body, '$.membership')='object'
  AND COALESCE(json_extract(body, '$.membership.origin'),'') NOT LIKE 'tcp://%';
UPDATE controlplane_state
SET body=json_set(body, '$.pending.origin', '')
WHERE json_type(body, '$.pending')='object'
  AND COALESCE(json_extract(body, '$.pending.origin'),'') NOT LIKE 'tcp://%';
