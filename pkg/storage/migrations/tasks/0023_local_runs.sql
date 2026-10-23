-- +migrate Up
-- +migrate StatementBegin
CREATE TABLE run_workspaces(control_id TEXT NOT NULL,task_id TEXT NOT NULL,run_id TEXT NOT NULL,body TEXT NOT NULL,PRIMARY KEY(control_id,task_id,run_id));
CREATE TABLE task_runs(control_id TEXT NOT NULL,task_id TEXT NOT NULL,run_id TEXT NOT NULL,digest TEXT NOT NULL,phase TEXT NOT NULL,boot_id TEXT NOT NULL,body TEXT NOT NULL,PRIMARY KEY(control_id,task_id,run_id));
CREATE TABLE task_events(control_id TEXT NOT NULL,task_id TEXT NOT NULL,run_id TEXT NOT NULL,sequence INTEGER NOT NULL,event TEXT NOT NULL,PRIMARY KEY(control_id,task_id,run_id,sequence));
CREATE TABLE retired_execution_receipts(control_id TEXT,task_id TEXT,run_id TEXT,command_id TEXT,digest TEXT,phase TEXT,reply TEXT,input TEXT);
CREATE TABLE journal_file_import(singleton INTEGER PRIMARY KEY CHECK(singleton=1));
INSERT INTO journal_file_import VALUES(1);
UPDATE tasks SET body=json_remove(json_set(body,'$.schema_version',11),'$.launch_sent') WHERE json_extract(body,'$.schema_version')=10;
UPDATE task_run_history SET body=json_remove(json_set(body,'$.schema_version',11),'$.launch_sent') WHERE json_extract(body,'$.schema_version')=10;
-- +migrate StatementEnd
