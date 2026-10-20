-- +migrate Up
-- +migrate StatementBegin
-- Established links already authorize nodes. Retain their keys, IDs and history,
-- replacing only the obsolete assistant presentation anchors. Pending admissions
-- retain their original metadata and require a new invitation instead of replay.
UPDATE agent_link_connections SET metadata=json_set(metadata,
 '$.schema_version',5,
 '$.local.address.kind','node','$.local.address.agent_id',json_extract(metadata,'$.local.address.node_id'),
 '$.peer.address.kind','node','$.peer.address.agent_id',json_extract(metadata,'$.peer.address.node_id'))
 WHERE json_extract(metadata,'$.schema_version')=4 AND json_extract(metadata,'$.last_seen') IS NOT NULL;
UPDATE agent_link_connections SET state='offline',metadata=json_set(metadata,'$.state','offline','$.last_error','agent_link_reconnect_required')
 WHERE state!='revoked' AND json_extract(metadata,'$.schema_version')!=5;
UPDATE agent_link_invitations SET expires_at=0;
ALTER TABLE agent_link_chat ADD COLUMN sender_kind TEXT NOT NULL DEFAULT 'assistant';
CREATE TABLE agent_link_chat_sessions(link_id TEXT NOT NULL,assistant_id TEXT NOT NULL,id TEXT NOT NULL UNIQUE,PRIMARY KEY(link_id,assistant_id));
-- +migrate StatementEnd
