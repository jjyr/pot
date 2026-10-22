-- +migrate Up
-- The receiver's opaque execution generation is not a Pot conversation UUID.
-- Remove the invalid cached projection; status reconciliation rebuilds the
-- local run UUID without rerunning or renotifying already delivered work.
UPDATE agent_link_delegations SET body=json_remove(body,'$.info.run_id')
WHERE json_extract(body,'$.request.executor')='codex'
 AND length(json_extract(body,'$.info.run_id'))=64;
