-- +migrate Up
-- +migrate StatementBegin
CREATE TABLE IF NOT EXISTS run_workspaces(control_id TEXT NOT NULL,task_id TEXT NOT NULL,run_id TEXT NOT NULL,body TEXT NOT NULL,PRIMARY KEY(control_id,task_id,run_id));
CREATE TABLE IF NOT EXISTS task_runs(control_id TEXT NOT NULL,task_id TEXT NOT NULL,run_id TEXT NOT NULL,digest TEXT NOT NULL,phase TEXT NOT NULL,boot_id TEXT NOT NULL,body TEXT NOT NULL,PRIMARY KEY(control_id,task_id,run_id));
CREATE TABLE IF NOT EXISTS task_events(control_id TEXT NOT NULL,task_id TEXT NOT NULL,run_id TEXT NOT NULL,sequence INTEGER NOT NULL,event TEXT NOT NULL,PRIMARY KEY(control_id,task_id,run_id,sequence));
CREATE TABLE IF NOT EXISTS run_command_inputs(control_id TEXT NOT NULL,task_id TEXT NOT NULL,run_id TEXT NOT NULL,command_id TEXT NOT NULL,body TEXT NOT NULL,PRIMARY KEY(control_id,task_id,run_id,command_id));
CREATE TABLE IF NOT EXISTS run_resources(control_id TEXT NOT NULL,task_id TEXT NOT NULL,run_id TEXT NOT NULL,digest TEXT NOT NULL,body TEXT NOT NULL,PRIMARY KEY(control_id,task_id,run_id));
CREATE TABLE IF NOT EXISTS run_commands(control_id TEXT NOT NULL,task_id TEXT NOT NULL,run_id TEXT NOT NULL,command_id TEXT NOT NULL,digest TEXT NOT NULL,phase TEXT NOT NULL,reply TEXT,PRIMARY KEY(control_id,task_id,run_id,command_id));
DROP TABLE IF EXISTS runner_schema;
-- +migrate StatementEnd
