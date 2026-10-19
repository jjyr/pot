-- +migrate Up
UPDATE agent_link_host SET mode='address' WHERE mode='https';
