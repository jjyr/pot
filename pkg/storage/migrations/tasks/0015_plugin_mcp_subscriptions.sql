-- +migrate Up
-- +migrate StatementBegin
-- The former binding is now a plugin-owned collection. Retaining the root row
-- with initialized=true is also the durable "do not recreate defaults" marker.
-- Migration 0014 already narrows ambiguous broad defaults; explicit nonempty
-- selections, the original recipient and disablement survive here.
UPDATE plugin_background
SET config=json_object('initialized',json('true'),'subscriptions',json_array(json_object(
 'id','legacy-github','assistant',assistant,'enabled',json(CASE WHEN enabled=1 THEN 'true' ELSE 'false' END),
 'account_id',COALESCE(json_extract(config,'$.account_id'),''),'revision',1,
 'changed_at',COALESCE(json_extract(state,'$.filterSince'),''),
 'filters',json_remove(config,'$.account_id')))),
 assistant='',enabled=1,revision=revision+1,
 state=CASE WHEN json_type(state)='object' THEN json_set(state,'$.pending',json('{}'),'$.scan',NULL,'$.modified',NULL) ELSE state END
WHERE id='github' AND plugin_id='github';
-- +migrate StatementEnd
