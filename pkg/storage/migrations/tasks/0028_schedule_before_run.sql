-- +migrate Up
-- +migrate StatementBegin
-- Existing definitions have no preflight script. Preserve every identity,
-- generation, due time, history and immutable write digest.
UPDATE schedules SET body=json_set(body,'$.info.spec.before_run',json('null'))
 WHERE json_type(body,'$.info.spec')='object';
UPDATE schedule_writes SET reply=json_set(reply,'$.spec.before_run',json('null'))
 WHERE json_type(reply,'$.spec')='object';
UPDATE schedule_runs SET body=json_set(body,'$.hook',json('null'));
CREATE TABLE schedule_probes (
 assistant_id TEXT NOT NULL,
 schedule_id TEXT NOT NULL,
 occurrence_id TEXT NOT NULL,
 body TEXT NOT NULL,
 PRIMARY KEY(assistant_id,schedule_id,occurrence_id)
);
-- +migrate StatementEnd
