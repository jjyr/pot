-- +migrate Up
-- +migrate StatementBegin
ALTER TABLE agent_messages ADD COLUMN content_estimated_tokens INTEGER CHECK(content_estimated_tokens >= 0);
ALTER TABLE agent_messages ADD COLUMN content_estimate_method TEXT;
-- +migrate StatementEnd
