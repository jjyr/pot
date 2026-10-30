-- +migrate Up
-- +migrate StatementBegin
CREATE INDEX agent_messages_usage_time ON agent_messages(accepted_at_ms,id);
-- +migrate StatementEnd
