-- +migrate Up
-- Historical envelopes have no trustworthy acceptance time. Keep zero as
-- unknown; retries preserve the timestamp of the original durable insertion.
ALTER TABLE agent_messages ADD COLUMN accepted_at_ms INTEGER NOT NULL DEFAULT 0;
CREATE INDEX agent_messages_outbox_route ON agent_messages(
 direction,json_extract(envelope,'$.from.node_id'),
 json_extract(envelope,'$.to.node_id'),accepted_at_ms
) WHERE state='queued' AND json_extract(envelope,'$.kind')='message';
