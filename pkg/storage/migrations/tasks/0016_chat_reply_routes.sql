-- +migrate Up
CREATE INDEX agent_chat_replies ON agent_messages(direction,json_extract(envelope,'$.reply_to'),sequence) WHERE json_extract(envelope,'$.kind')='message';
