-- +migrate Up
-- +migrate StatementBegin
ALTER TABLE tools ADD COLUMN call_estimated_tokens INTEGER CHECK(call_estimated_tokens >= 0);
ALTER TABLE tools ADD COLUMN result_estimated_tokens INTEGER CHECK(result_estimated_tokens >= 0);
ALTER TABLE tools ADD COLUMN call_estimate_method TEXT;
ALTER TABLE tools ADD COLUMN result_estimate_method TEXT;
-- +migrate StatementEnd
