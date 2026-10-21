-- +migrate Up
-- Typed user-to-assistant chat and correlated assistant-to-user replies use the
-- existing immutable envelope. Old rows remain byte-for-byte unchanged.
CREATE INDEX agent_link_user_chat_routes ON agent_messages(json_extract(envelope,'$.to.node_id'),json_extract(envelope,'$.to.kind'),json_extract(envelope,'$.to.agent_id')) WHERE json_extract(envelope,'$.kind')='message';
