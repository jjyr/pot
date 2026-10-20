-- +migrate Up
-- +migrate StatementBegin
-- Convert calendar definitions and saved write responses together. Keep due times,
-- generations, Task/run identities and receipt digests unchanged.
CREATE TEMP VIEW cron_schedule_specs AS
SELECT 'plan' AS source, rowid AS record, json_extract(body,'$.info.spec') AS spec FROM schedules
UNION ALL
SELECT 'reply', rowid, json_extract(reply,'$.spec') FROM schedule_writes WHERE json_type(reply,'$.spec')='object';
CREATE TEMP VIEW cron_schedule_converted AS
SELECT source, record, json_remove(
 CASE json_extract(spec,'$.kind')
 WHEN 'daily' THEN json_set(spec,'$.kind','cron','$.cron',
   printf('%d %d * * *', CAST(substr(json_extract(spec,'$.time'),4,2) AS INTEGER), CAST(substr(json_extract(spec,'$.time'),1,2) AS INTEGER)))
 WHEN 'weekly' THEN json_set(spec,'$.kind','cron','$.cron',
   printf('%d %d * * %s', CAST(substr(json_extract(spec,'$.time'),4,2) AS INTEGER), CAST(substr(json_extract(spec,'$.time'),1,2) AS INTEGER),
     (SELECT group_concat(CASE CAST(value AS INTEGER) WHEN 7 THEN 0 ELSE CAST(value AS INTEGER) END,',') FROM json_each(spec,'$.weekdays'))))
 ELSE spec END, '$.time','$.weekdays') AS spec FROM cron_schedule_specs;
UPDATE schedules SET body=json_set(body,'$.info.spec',json((SELECT spec FROM cron_schedule_converted WHERE source='plan' AND record=schedules.rowid)));
UPDATE schedule_writes SET reply=json_set(reply,'$.spec',json((SELECT spec FROM cron_schedule_converted WHERE source='reply' AND record=schedule_writes.rowid))) WHERE json_type(reply,'$.spec')='object';
DROP VIEW cron_schedule_converted;
DROP VIEW cron_schedule_specs;
-- +migrate StatementEnd
