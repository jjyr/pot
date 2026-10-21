-- +migrate Up
-- Cached remote directories acquire an optional avatar. Unknown old images stay
-- empty until an authenticated directory refresh supplies the receiver's image.
UPDATE agent_link_connections
SET metadata=json_set(metadata,'$.directory.assistants',json((
 SELECT COALESCE(json_group_array(json_set(value,'$.avatar',COALESCE(json_extract(value,'$.avatar'),''))),'[]')
 FROM json_each(metadata,'$.directory.assistants')
)))
WHERE json_type(metadata,'$.directory.assistants')='array';
