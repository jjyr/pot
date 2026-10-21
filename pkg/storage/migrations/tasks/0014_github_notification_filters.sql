-- +migrate Up
-- +migrate StatementBegin
CREATE TABLE plugin_background_writes (
 instance TEXT NOT NULL,
 request_id TEXT NOT NULL,
 input_hash TEXT NOT NULL,
 PRIMARY KEY(instance,request_id)
);
-- The old format cannot distinguish an intentional all-reasons selection from
-- its broad default. Conservatively narrow only that ambiguous selection.
-- Explicit nonempty filters, disablement, routes, cursors and receipts survive.
UPDATE plugin_background SET
 config=json_object(
   'account_id',COALESCE(json_extract(state,'$.account'),''),
   'repositories',CASE WHEN json_array_length(config,'$.repositories')>0 THEN json_extract(config,'$.repositories') ELSE NULL END,
   'reasons',CASE WHEN json_array_length(config,'$.reasons')>0 THEN json_extract(config,'$.reasons') ELSE json('["mention"]') END,
   'subject_types',NULL,'threads',NULL),
 state=CASE WHEN json_type(state)='object' THEN json_set(json_remove(state,'$.repositoryModified'),'$.pending',json('{}'),'$.scan',NULL,'$.modified',NULL,'$.filterSince',NULL) ELSE state END,
 revision=revision+1
WHERE id='github';
-- +migrate StatementEnd
