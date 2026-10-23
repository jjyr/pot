-- +migrate Up
INSERT INTO metadata(key,value) VALUES ('conversation.completion','null')
ON CONFLICT(key) DO NOTHING;
