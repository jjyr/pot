package storage

import (
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestAgentAdapterUpgradePreservesCodexIdentityAndSessions(t *testing.T) {
	for _, scenario := range []struct {
		kind         Database
		messageFiles bool
	}{
		{kind: ControlDatabase},
		{kind: ControlDatabase, messageFiles: true},
		{kind: TasksDatabase},
	} {
		name := string(scenario.kind)
		if scenario.messageFiles {
			name += "/message-files"
		}
		t.Run(name, func(t *testing.T) {
			kind := scenario.kind
			path := filepath.Join(t.TempDir(), "db.sqlite")
			db, err := OpenSQLite(path)
			if err != nil {
				t.Fatal(err)
			}
			prior := initialSchema(t, kind)
			if scenario.messageFiles {
				name := "migrations/control/0002_message_files.sql"
				body, err := migrationFiles.ReadFile(name)
				if err != nil {
					t.Fatal(err)
				}
				prior[name] = &fstest.MapFile{Data: body}
			}
			if err = migrateDatabase(t.Context(), db, kind, prior); err != nil {
				t.Fatal(err)
			}
			if scenario.messageFiles {
				execMigrationSQL(t, db, `INSERT INTO message_files VALUES('owner','chat','message','revision','saved-ref',0,1,'file.txt','file',0,'{}','pending','')`)
			}
			if kind == ControlDatabase {
				execMigrationSQL(t, db, `INSERT INTO codex_agent_host(singleton,node_id,owner_secret_ref,endpoint,peer_public_key,peer_secret_ref) VALUES(1,'original-node','owner-ref','tcp://127.0.0.1:8793','original-public','key-ref'); INSERT INTO codex_agent_endpoints(endpoint) VALUES('tcp://127.0.0.1:8793')`)
			} else {
				execMigrationSQL(t, db, `INSERT INTO codex_link_assistants(id,name,workspace,mode,thread_id,revision) VALUES('original','Original','/workspace','read-only','original-thread',7); INSERT INTO codex_link_jobs(id,caller,request_id,assistant_id,payload,data) VALUES('task','caller','request','original','unchanged-request','{"cleanup_uncertain":true}'); INSERT INTO codex_task_sessions(task_id,workspace,mode,thread_id) VALUES('task','/workspace','read-only','task-thread'); INSERT INTO codex_task_inputs VALUES('task','input','text','prior-run'); INSERT INTO codex_task_executor VALUES(1,'/workspace','read-only')`)
			}
			if err = Migrate(t.Context(), db, kind); err != nil {
				t.Fatal(err)
			}
			db.Close()
			db, err = OpenDatabase(path, kind)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if kind == ControlDatabase {
				var id, adapter string
				if err = db.QueryRow("SELECT node_id,adapter FROM agent_adapter_host").Scan(&id, &adapter); err != nil || id != "original-node" || adapter != "codex" {
					t.Fatal(id, adapter, err)
				}
				if scenario.messageFiles {
					var ref string
					if err = db.QueryRow("SELECT ref FROM message_files").Scan(&ref); err != nil || ref != "saved-ref" {
						t.Fatal("message file lost during adapter upgrade", ref, err)
					}
				}
			} else {
				var thread, model, payload, data string
				var revision int
				if err = db.QueryRow("SELECT thread_id,model,revision FROM agent_adapter_assistants").Scan(&thread, &model, &revision); err != nil || thread != "original-thread" || model != "" || revision != 7 {
					t.Fatal(thread, model, revision, err)
				}
				if err = db.QueryRow("SELECT payload,data FROM agent_adapter_jobs").Scan(&payload, &data); err != nil || payload != "unchanged-request" || data != `{"cleanup_uncertain":true}` {
					t.Fatal(payload, data, err)
				}
				if err = db.QueryRow("SELECT thread_id FROM agent_adapter_sessions").Scan(&thread); err != nil || thread != "task-thread" {
					t.Fatal(thread, err)
				}
			}
			rows, e := db.Query("PRAGMA foreign_key_check")
			if e != nil {
				t.Fatal(e)
			}
			defer rows.Close()
			if rows.Next() {
				t.Fatal("migration broke foreign key bindings")
			}
		})
	}
}
