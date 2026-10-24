-- +migrate Up
-- +migrate StatementBegin
-- First public schema baseline; pre-release databases are intentionally unsupported.
CREATE TABLE "resource_file_export"(control_id TEXT NOT NULL,task_id TEXT NOT NULL,run_id TEXT NOT NULL,digest TEXT NOT NULL,body TEXT NOT NULL,PRIMARY KEY(control_id,task_id,run_id));

CREATE TABLE retired_execution_receipts(
  control_id TEXT,
  task_id TEXT,
  run_id TEXT,
  command_id TEXT,
  digest TEXT,
  phase TEXT,
  reply TEXT,
  input TEXT
);

CREATE TABLE run_workspaces(control_id TEXT NOT NULL,task_id TEXT NOT NULL,run_id TEXT NOT NULL,body TEXT NOT NULL,PRIMARY KEY(control_id,task_id,run_id));

CREATE TABLE task_events(control_id TEXT NOT NULL,task_id TEXT NOT NULL,run_id TEXT NOT NULL,sequence INTEGER NOT NULL,event TEXT NOT NULL,PRIMARY KEY(control_id,task_id,run_id,sequence));

CREATE TABLE task_runs(control_id TEXT NOT NULL,task_id TEXT NOT NULL,run_id TEXT NOT NULL,digest TEXT NOT NULL,phase TEXT NOT NULL,boot_id TEXT NOT NULL,body TEXT NOT NULL,PRIMARY KEY(control_id,task_id,run_id));

CREATE UNIQUE INDEX task_events_task_sequence ON task_events(control_id,task_id,sequence);

-- +migrate StatementEnd
