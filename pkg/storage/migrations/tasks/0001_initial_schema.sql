-- +migrate Up
-- +migrate StatementBegin
-- First public schema baseline; pre-release databases are intentionally unsupported.
CREATE TABLE agent_delivery_items(batch TEXT NOT NULL,message TEXT NOT NULL UNIQUE,PRIMARY KEY(batch,message));

CREATE TABLE agent_messages(
 sequence INTEGER PRIMARY KEY AUTOINCREMENT,id TEXT NOT NULL UNIQUE,
 envelope TEXT NOT NULL,direction TEXT NOT NULL,state TEXT NOT NULL,
 conversation TEXT NOT NULL,recipient TEXT NOT NULL,detail TEXT, accepted_at_ms INTEGER NOT NULL DEFAULT 0);

CREATE TABLE agent_result_observations(owner TEXT NOT NULL,turn TEXT NOT NULL,message TEXT NOT NULL,PRIMARY KEY(owner,turn,message));

CREATE TABLE assistant_contacts(node TEXT PRIMARY KEY,directory TEXT NOT NULL);

CREATE TABLE assistant_mail_events(sequence INTEGER PRIMARY KEY AUTOINCREMENT,message TEXT NOT NULL,event TEXT NOT NULL);

CREATE TABLE assistant_mail_identity(singleton INTEGER PRIMARY KEY CHECK(singleton=1),node_id TEXT NOT NULL);

CREATE TABLE assistant_mail_models(message TEXT NOT NULL,request TEXT NOT NULL,PRIMARY KEY(message,request));

CREATE TABLE assistant_mail_sessions(assistant TEXT NOT NULL,conversation TEXT NOT NULL,config TEXT NOT NULL,context TEXT,PRIMARY KEY(assistant,conversation));

CREATE TABLE codex_link_assistants(
 id TEXT PRIMARY KEY,
 name TEXT NOT NULL,
 workspace TEXT NOT NULL,
 mode TEXT NOT NULL CHECK(mode IN ('read-only','workspace-write')),
 thread_id TEXT NOT NULL DEFAULT '',
 is_default INTEGER NOT NULL DEFAULT 0 CHECK(is_default IN (0,1))
, avatar TEXT NOT NULL DEFAULT '', character TEXT NOT NULL DEFAULT '', revision INTEGER NOT NULL DEFAULT 1, deleted INTEGER NOT NULL DEFAULT 0 CHECK(deleted IN (0,1)));

CREATE TABLE "codex_link_jobs" (
 sequence INTEGER PRIMARY KEY AUTOINCREMENT,
 id TEXT NOT NULL UNIQUE,
 caller TEXT NOT NULL,
 request_id TEXT NOT NULL,
 assistant_id TEXT REFERENCES codex_link_assistants(id),
 payload TEXT NOT NULL,
 data TEXT NOT NULL,
 UNIQUE(caller,request_id)
);

CREATE TABLE codex_mail_consumers(id TEXT PRIMARY KEY,parent_id TEXT,active INTEGER NOT NULL DEFAULT 1);

CREATE TABLE codex_task_executor (
 singleton INTEGER PRIMARY KEY CHECK(singleton=1),
 workspace TEXT NOT NULL,
 mode TEXT NOT NULL CHECK(mode IN ('read-only','workspace-write'))
);

CREATE TABLE codex_task_inputs (
 task_id TEXT NOT NULL REFERENCES codex_link_jobs(id),
 request_id TEXT NOT NULL,
 text TEXT NOT NULL,
 previous_run TEXT NOT NULL,
 PRIMARY KEY(task_id,request_id)
);

CREATE TABLE codex_task_sessions (
 task_id TEXT PRIMARY KEY REFERENCES codex_link_jobs(id),
 workspace TEXT NOT NULL,
 mode TEXT NOT NULL CHECK(mode IN ('read-only','workspace-write')),
 thread_id TEXT NOT NULL DEFAULT ''
);

CREATE TABLE journal_file_import(singleton INTEGER PRIMARY KEY CHECK(singleton=1));

CREATE TABLE owner_resource_commands(id TEXT PRIMARY KEY,digest TEXT NOT NULL,scope TEXT NOT NULL,receipt TEXT NOT NULL);

CREATE TABLE plugin_background(id TEXT PRIMARY KEY, enabled INTEGER NOT NULL, assistant TEXT NOT NULL, config TEXT NOT NULL, revision INTEGER NOT NULL, state TEXT NOT NULL, next INTEGER NOT NULL, status TEXT NOT NULL, conversation TEXT NOT NULL, diagnostic TEXT NOT NULL DEFAULT '', plugin_id TEXT NOT NULL DEFAULT '', connection_id TEXT NOT NULL DEFAULT '');

CREATE TABLE plugin_background_writes (
 instance TEXT NOT NULL,
 request_id TEXT NOT NULL,
 input_hash TEXT NOT NULL,
 PRIMARY KEY(instance,request_id)
);

CREATE TABLE retired_execution_receipts(control_id TEXT,task_id TEXT,run_id TEXT,command_id TEXT,digest TEXT,phase TEXT,reply TEXT,input TEXT);

CREATE TABLE "retired_task_authority"(control_id TEXT NOT NULL,runtime_id TEXT NOT NULL);

CREATE TABLE run_workspaces(control_id TEXT NOT NULL,task_id TEXT NOT NULL,run_id TEXT NOT NULL,body TEXT NOT NULL,PRIMARY KEY(control_id,task_id,run_id));

CREATE TABLE schedule_probes (
 assistant_id TEXT NOT NULL,
 schedule_id TEXT NOT NULL,
 occurrence_id TEXT NOT NULL,
 body TEXT NOT NULL,
 PRIMARY KEY(assistant_id,schedule_id,occurrence_id)
);

CREATE TABLE schedule_runs(assistant_id TEXT NOT NULL,schedule_id TEXT NOT NULL,id TEXT NOT NULL,scheduled INTEGER NOT NULL,finished INTEGER,body TEXT NOT NULL,PRIMARY KEY(assistant_id,schedule_id,id));

CREATE TABLE schedule_writes(assistant_id TEXT NOT NULL,request_id TEXT NOT NULL,digest TEXT NOT NULL,reply TEXT NOT NULL,PRIMARY KEY(assistant_id,request_id));

CREATE TABLE schedules(assistant_id TEXT NOT NULL,id TEXT NOT NULL,body TEXT NOT NULL,PRIMARY KEY(assistant_id,id));

CREATE TABLE task_event_cache(task_id TEXT NOT NULL,sequence INTEGER NOT NULL,event TEXT NOT NULL,PRIMARY KEY(task_id,sequence));

CREATE TABLE task_events(control_id TEXT NOT NULL,task_id TEXT NOT NULL,run_id TEXT NOT NULL,sequence INTEGER NOT NULL,event TEXT NOT NULL,PRIMARY KEY(control_id,task_id,run_id,sequence));

CREATE TABLE task_input_digest_formats(message TEXT PRIMARY KEY REFERENCES agent_messages(id),format INTEGER NOT NULL CHECK(format=1));

CREATE TABLE task_node(singleton INTEGER PRIMARY KEY CHECK(singleton=1),node_id TEXT NOT NULL);

CREATE TABLE task_outcomes(
 sequence INTEGER PRIMARY KEY AUTOINCREMENT CHECK(sequence<=9007199254740991),
 assistant_id TEXT NOT NULL,task_id TEXT NOT NULL,title TEXT NOT NULL,
 phase TEXT NOT NULL,updated_at_ms INTEGER NOT NULL, run_id TEXT NOT NULL DEFAULT '');

CREATE TABLE task_retries(assistant_id TEXT NOT NULL,source_task TEXT NOT NULL,task_id TEXT NOT NULL UNIQUE,PRIMARY KEY(assistant_id,source_task));

CREATE TABLE task_routing(singleton INTEGER PRIMARY KEY CHECK(singleton=1),body TEXT NOT NULL);

CREATE TABLE task_run_history(assistant_id TEXT NOT NULL,task_id TEXT NOT NULL,run_id TEXT NOT NULL,body TEXT NOT NULL,PRIMARY KEY(assistant_id,task_id,run_id));

CREATE TABLE task_runs(control_id TEXT NOT NULL,task_id TEXT NOT NULL,run_id TEXT NOT NULL,digest TEXT NOT NULL,phase TEXT NOT NULL,boot_id TEXT NOT NULL,body TEXT NOT NULL,PRIMARY KEY(control_id,task_id,run_id));

CREATE TABLE task_submission_import(singleton INTEGER PRIMARY KEY CHECK(singleton=1));

CREATE TABLE task_writes(assistant_id TEXT NOT NULL,request_id TEXT NOT NULL,digest TEXT NOT NULL,task_id TEXT NOT NULL,PRIMARY KEY(assistant_id,request_id));

CREATE TABLE tasks(assistant_id TEXT NOT NULL,id TEXT NOT NULL,phase TEXT NOT NULL,created INTEGER NOT NULL,body TEXT NOT NULL,PRIMARY KEY(assistant_id,id));

INSERT INTO "journal_file_import" VALUES (1);
INSERT INTO "task_submission_import" VALUES (1);
CREATE INDEX agent_chat_replies ON agent_messages(direction,json_extract(envelope,'$.reply_to'),sequence) WHERE json_extract(envelope,'$.kind')='message';

CREATE INDEX agent_link_user_chat_routes ON agent_messages(json_extract(envelope,'$.to.node_id'),json_extract(envelope,'$.to.kind'),json_extract(envelope,'$.to.agent_id')) WHERE json_extract(envelope,'$.kind')='message';

CREATE INDEX agent_messages_context ON agent_messages(conversation,recipient,state);

CREATE INDEX agent_messages_outbox_route ON agent_messages(
 direction,json_extract(envelope,'$.from.node_id'),
 json_extract(envelope,'$.to.node_id'),accepted_at_ms
) WHERE state='queued' AND json_extract(envelope,'$.kind')='message';

CREATE INDEX agent_messages_recipient ON agent_messages(recipient,direction,state,sequence);

CREATE INDEX agent_plugin_instance_state ON agent_messages(json_extract(envelope,'$.plugin.instance_id'),state);

CREATE INDEX agent_plugin_state ON agent_messages(json_extract(envelope,'$.plugin.identity.id'),state);

CREATE UNIQUE INDEX codex_link_default ON codex_link_assistants(is_default) WHERE is_default=1;

CREATE INDEX task_outcomes_owner_sequence ON task_outcomes(assistant_id,sequence);

CREATE UNIQUE INDEX task_outcomes_run ON task_outcomes(assistant_id,task_id,run_id,phase) WHERE run_id!='';

CREATE INDEX tasks_catalog_order ON tasks(CASE WHEN json_valid(body) THEN json_extract(body,'$.info.updated_at_ms') END DESC,id,assistant_id);

CREATE INDEX tasks_catalog_phase ON tasks(phase);

CREATE INDEX tasks_owner_order ON tasks(assistant_id,CASE WHEN json_valid(body) THEN json_extract(body,'$.info.updated_at_ms') END DESC,id);

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

CREATE VIEW task_notifications AS SELECT sequence,id,recipient AS assistant_id,
 json_extract(envelope,'$.result.task_id') AS task_id,
 json_extract(envelope,'$.result.phase') AS phase,
 json_extract(envelope,'$.result.text') AS text
 FROM agent_messages WHERE json_extract(envelope,'$.kind')='task_result' AND state='queued';

CREATE TRIGGER task_input_update INSTEAD OF UPDATE ON task_inputs BEGIN
 UPDATE agent_messages SET state=NEW.state WHERE sequence=OLD.sequence;
END;

CREATE TRIGGER task_notification_delete INSTEAD OF DELETE ON task_notifications BEGIN
 UPDATE agent_messages SET state='processed' WHERE id=OLD.id AND state='queued';
 END;

-- +migrate StatementEnd
