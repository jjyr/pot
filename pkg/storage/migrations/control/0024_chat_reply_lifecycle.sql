-- +migrate Up
-- Request projections retain execution facts independently of send receipts.
ALTER TABLE agent_link_chat ADD COLUMN reply_to TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_link_chat ADD COLUMN reply_state TEXT NOT NULL DEFAULT 'pending';
ALTER TABLE agent_link_chat ADD COLUMN reply_error TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_link_chat ADD COLUMN reply_uncertain INTEGER NOT NULL DEFAULT 0;
ALTER TABLE agent_link_chat ADD COLUMN checked_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE agent_link_chat_sessions ADD COLUMN revision INTEGER NOT NULL DEFAULT 1;
UPDATE agent_link_chat SET reply_state='unknown',reply_error='agent_link_historical_outcome_unknown' WHERE role='user' AND sender_kind!='user';
-- Recover exact reply bindings from immutable mailbox envelopes when available.
-- The tasks database is independent; Control fills historical bindings on startup.
CREATE INDEX agent_link_chat_replies ON agent_link_chat(reply_to);
