-- +migrate Up
-- +migrate StatementBegin
CREATE TABLE model_prices(provider TEXT NOT NULL,profile_id TEXT NOT NULL,model TEXT NOT NULL, effective_at INTEGER NOT NULL CHECK(effective_at>=0), price TEXT NOT NULL, PRIMARY KEY(provider,profile_id,model,effective_at));
CREATE TABLE request_accounting(request_id TEXT PRIMARY KEY REFERENCES requests(id), assistant_id TEXT NOT NULL DEFAULT '', task_id TEXT NOT NULL DEFAULT '', correlation_id TEXT NOT NULL DEFAULT '', stage TEXT NOT NULL, effort TEXT NOT NULL DEFAULT '', price TEXT, cost_usd TEXT, cost_status TEXT NOT NULL DEFAULT 'unpriced' CHECK(cost_status IN ('unpriced','usage_missing','priced')), cache_read INTEGER CHECK(cache_read>=0), cache_write INTEGER CHECK(cache_write>=0), reasoning INTEGER CHECK(reasoning>=0), CHECK((cost_status='priced' AND cost_usd IS NOT NULL AND price IS NOT NULL) OR (cost_status<>'priced' AND cost_usd IS NULL)));
CREATE INDEX accounting_assistant ON request_accounting(assistant_id,request_id);
CREATE INDEX accounting_task ON request_accounting(task_id,request_id);
CREATE INDEX accounting_correlation ON request_accounting(correlation_id,request_id);
-- +migrate StatementEnd
