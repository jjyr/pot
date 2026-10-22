-- +migrate Up
-- +migrate StatementBegin
-- Codex no longer consumes direct user chats. Settle their pending inbox rows
-- once so removing that execution path cannot block assistant message admission.
-- Immutable envelopes, accepted payloads, completed results and thread IDs stay.
UPDATE agent_messages SET state='processed',detail='agent_link_capability_unsupported'
WHERE direction='incoming' AND state IN ('queued','read')
 AND json_extract(envelope,'$.from.kind')='user';

UPDATE codex_link_jobs SET data=json_set(data,
 '$.task.state','failed','$.task.error','agent_link_capability_unsupported')
WHERE json_extract(data,'$.message.from.kind')='user'
 AND json_extract(data,'$.task.state')='queued';
-- +migrate StatementEnd
