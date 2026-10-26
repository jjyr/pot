-- +migrate Up
-- Existing sources retain their original confined policy (missing rules means
-- v1). Do not rewrite their JSON: it participates in immutable receipt hashes.
-- The ledger records the new persisted Source.rules contract; no history reads,
-- projection retries or authorization upgrades occur during this migration.
SELECT 1;
