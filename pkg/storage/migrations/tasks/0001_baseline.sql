-- +migrate Up
-- +migrate StatementBegin
CREATE TABLE IF NOT EXISTS tasks(assistant_id TEXT NOT NULL,id TEXT NOT NULL,phase TEXT NOT NULL,created INTEGER NOT NULL,body TEXT NOT NULL,PRIMARY KEY(assistant_id,id));
CREATE TABLE IF NOT EXISTS task_writes(assistant_id TEXT NOT NULL,request_id TEXT NOT NULL,digest TEXT NOT NULL,task_id TEXT NOT NULL,PRIMARY KEY(assistant_id,request_id));
CREATE TABLE IF NOT EXISTS task_retries(assistant_id TEXT NOT NULL,source_task TEXT NOT NULL,task_id TEXT NOT NULL UNIQUE,PRIMARY KEY(assistant_id,source_task));
CREATE TABLE IF NOT EXISTS task_authority(control_id TEXT NOT NULL,runtime_id TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS agent_messages(
 sequence INTEGER PRIMARY KEY AUTOINCREMENT,id TEXT NOT NULL UNIQUE,
 envelope TEXT NOT NULL,direction TEXT NOT NULL,state TEXT NOT NULL,
 conversation TEXT NOT NULL,recipient TEXT NOT NULL,detail TEXT);
CREATE INDEX IF NOT EXISTS agent_messages_recipient ON agent_messages(recipient,direction,state,sequence);
CREATE INDEX IF NOT EXISTS agent_messages_context ON agent_messages(conversation,recipient,state);
CREATE TABLE IF NOT EXISTS agent_result_observations(owner TEXT NOT NULL,turn TEXT NOT NULL,message TEXT NOT NULL,PRIMARY KEY(owner,turn,message));
CREATE TABLE IF NOT EXISTS agent_delivery_items(batch TEXT NOT NULL,message TEXT NOT NULL UNIQUE,PRIMARY KEY(batch,message));

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
 END;
CREATE TABLE IF NOT EXISTS task_outcomes(
 sequence INTEGER PRIMARY KEY AUTOINCREMENT CHECK(sequence<=9007199254740991),
 assistant_id TEXT NOT NULL,task_id TEXT NOT NULL,title TEXT NOT NULL,
 phase TEXT NOT NULL,updated_at_ms INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS task_outcomes_owner_sequence ON task_outcomes(assistant_id,sequence);
CREATE TABLE IF NOT EXISTS task_event_cache(task_id TEXT NOT NULL,sequence INTEGER NOT NULL,event TEXT NOT NULL,PRIMARY KEY(task_id,sequence));
CREATE TABLE IF NOT EXISTS task_routing(singleton INTEGER PRIMARY KEY CHECK(singleton=1),body TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS owner_resource_commands(id TEXT PRIMARY KEY,digest TEXT NOT NULL,scope TEXT NOT NULL,receipt TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS schedules(assistant_id TEXT NOT NULL,id TEXT NOT NULL,body TEXT NOT NULL,PRIMARY KEY(assistant_id,id));
CREATE TABLE IF NOT EXISTS schedule_runs(assistant_id TEXT NOT NULL,schedule_id TEXT NOT NULL,id TEXT NOT NULL,scheduled INTEGER NOT NULL,finished INTEGER,body TEXT NOT NULL,PRIMARY KEY(assistant_id,schedule_id,id));
CREATE TABLE IF NOT EXISTS schedule_writes(assistant_id TEXT NOT NULL,request_id TEXT NOT NULL,digest TEXT NOT NULL,reply TEXT NOT NULL,PRIMARY KEY(assistant_id,request_id));
CREATE TABLE IF NOT EXISTS assistant_mail_identity(singleton INTEGER PRIMARY KEY CHECK(singleton=1),control_id TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS assistant_mail_sessions(assistant TEXT NOT NULL,conversation TEXT NOT NULL,config TEXT NOT NULL,context TEXT,PRIMARY KEY(assistant,conversation));
CREATE TABLE IF NOT EXISTS assistant_mail_events(sequence INTEGER PRIMARY KEY AUTOINCREMENT,message TEXT NOT NULL,event TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS assistant_mail_models(message TEXT NOT NULL,request TEXT NOT NULL,PRIMARY KEY(message,request));
CREATE TABLE IF NOT EXISTS assistant_contacts(control TEXT PRIMARY KEY,directory TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS plugin_background(id TEXT PRIMARY KEY, enabled INTEGER NOT NULL, assistant TEXT NOT NULL, config TEXT NOT NULL, revision INTEGER NOT NULL, state TEXT NOT NULL, next INTEGER NOT NULL, status TEXT NOT NULL, conversation TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS agent_plugin_state ON agent_messages(json_extract(envelope,'$.plugin.identity.id'),state);
DROP TABLE IF EXISTS task_schema;
DROP TABLE IF EXISTS schedule_schema;
-- +migrate StatementEnd
