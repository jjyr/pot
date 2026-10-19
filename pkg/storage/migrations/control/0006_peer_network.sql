-- +migrate Up
-- Old HTTP endpoints cannot identify the new peer listener. Keep its stable
-- listener configuration, but require explicit verified TCP publication.
UPDATE agent_link_host
SET endpoint='', mode='address'
WHERE endpoint<>'' AND endpoint NOT LIKE 'tcp://%';
