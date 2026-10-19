-- +migrate Up
-- +migrate StatementBegin
-- A Task input stores an irreversible digest of its complete parent, which may
-- live outside this database. Retain the exact historical hash recipe for these
-- receipts rather than guessing or weakening request-content verification.
CREATE TABLE task_input_digest_formats(message TEXT PRIMARY KEY REFERENCES agent_messages(id),format INTEGER NOT NULL CHECK(format=1));
INSERT INTO task_input_digest_formats SELECT id,1 FROM agent_messages WHERE json_extract(envelope,'$.digest') IS NOT NULL AND json_extract(envelope,'$.to.kind')='task';
UPDATE assistant_mail_sessions SET conversation=(SELECT pot_node_correspondence(envelope,1) FROM agent_messages WHERE recipient=assistant_mail_sessions.assistant AND pot_node_correspondence(envelope,0)=assistant_mail_sessions.conversation LIMIT 1)
 WHERE EXISTS(SELECT 1 FROM agent_messages WHERE recipient=assistant_mail_sessions.assistant AND pot_node_correspondence(envelope,0)=assistant_mail_sessions.conversation);
UPDATE agent_messages SET envelope=pot_node_json(envelope,'envelope');
UPDATE assistant_contacts SET directory=pot_node_json(directory,'directory');
ALTER TABLE assistant_contacts RENAME COLUMN control TO node;
ALTER TABLE assistant_mail_identity RENAME COLUMN control_id TO node_id;
UPDATE tasks SET body=pot_node_json(body,'task');
UPDATE task_run_history SET body=pot_node_json(body,'task');
UPDATE assistant_mail_sessions SET context=pot_node_json(context,'checkpoint');
DROP TRIGGER task_input_update;
DROP VIEW task_inputs;
CREATE VIEW task_inputs AS SELECT sequence,
 json_extract(envelope,'$.owner') AS assistant_id,recipient AS task_id,
 json_extract(envelope,'$.request_id') AS request_id,
 json_extract(envelope,'$.digest') AS digest,
 json_extract(envelope,'$.kind') AS kind,
 json_extract(envelope,'$.from.agent_id') AS sender,
 json_extract(envelope,'$.from.kind') AS sender_kind,
 json_extract(envelope,'$.from.node_id') AS sender_node,
 json_extract(envelope,'$.text') AS text,state
 FROM agent_messages WHERE json_extract(envelope,'$.to.kind')='task';
CREATE TRIGGER task_input_update INSTEAD OF UPDATE ON task_inputs BEGIN
 UPDATE agent_messages SET state=NEW.state WHERE sequence=OLD.sequence;
END;
-- +migrate StatementEnd
