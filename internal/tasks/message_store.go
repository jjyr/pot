package tasks

import "database/sql"

// These are transcript projections, not additional queues. All message kinds
// live in agent_messages, including the initial Task input and terminal hook.
func initializeMessageViews(db *sql.DB) error {
	_, err := db.Exec(`
 CREATE VIEW IF NOT EXISTS task_inputs AS SELECT sequence,
 json_extract(envelope,'$.owner') AS assistant_id,recipient AS task_id,
 json_extract(envelope,'$.request_id') AS request_id,
 json_extract(envelope,'$.digest') AS digest,
 json_extract(envelope,'$.kind') AS kind,
 json_extract(envelope,'$.from.agent_id') AS sender,
 json_extract(envelope,'$.from.kind') AS sender_kind,
 json_extract(envelope,'$.from.control_id') AS sender_control,
 json_extract(envelope,'$.text') AS text,state
 FROM agent_messages WHERE json_extract(envelope,'$.to.kind')='task';
 CREATE TRIGGER IF NOT EXISTS task_input_update INSTEAD OF UPDATE ON task_inputs BEGIN
 UPDATE agent_messages SET state=NEW.state WHERE sequence=OLD.sequence;
 END;
 CREATE VIEW IF NOT EXISTS task_notifications AS SELECT sequence,id,recipient AS assistant_id,
 json_extract(envelope,'$.result.task_id') AS task_id,
 json_extract(envelope,'$.result.phase') AS phase,
 json_extract(envelope,'$.result.text') AS text
 FROM agent_messages WHERE json_extract(envelope,'$.kind')='task_result' AND state='queued';
 CREATE TRIGGER IF NOT EXISTS task_notification_delete INSTEAD OF DELETE ON task_notifications BEGIN
 UPDATE agent_messages SET state='processed' WHERE id=OLD.id AND state='queued';
 END;`)
	return err
}
