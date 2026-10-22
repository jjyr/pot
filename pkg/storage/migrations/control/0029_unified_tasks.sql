-- +migrate Up
-- +migrate StatementBegin
-- Startup imports these original submissions into tasks.sqlite before receivers start.
ALTER TABLE agent_link_delegations RENAME TO retired_task_submissions;
-- +migrate StatementEnd
