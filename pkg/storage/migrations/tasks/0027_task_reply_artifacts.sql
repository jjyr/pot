-- +migrate Up
-- +migrate StatementBegin
-- Final replies now carry scheduled summaries. Remove scheduler-owned comparison
-- state and migrate change-filtered delivery to ordinary terminal delivery.
UPDATE schedules SET body=json_remove(body,'$.info.baseline');
UPDATE schedules SET body=json_set(body,'$.info.spec.notify','all')
 WHERE json_extract(body,'$.info.spec.notify')='changes';
UPDATE schedule_runs SET body=json_remove(body,'$.changed');
UPDATE schedule_runs SET body=json_set(body,'$.notify','all')
 WHERE json_extract(body,'$.notify')='changes';
UPDATE schedule_writes SET reply=json_remove(reply,'$.baseline')
 WHERE json_type(reply,'$.spec')='object';
UPDATE schedule_writes SET reply=json_set(reply,'$.spec.notify','all')
 WHERE json_extract(reply,'$.spec.notify')='changes';
-- ArtifactsError is optional Task outcome metadata. Existing outcome envelopes,
-- delivery states and execution receipts are retained verbatim; no work replays.
-- +migrate StatementEnd
