package storage

import (
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestManagementScopesUpgradeRetainsRequestsAndReceipts(t *testing.T) {
	for _, skip := range []bool{false, true} {
		t.Run(map[bool]string{false: "previous", true: "skipped"}[skip], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "tasks.sqlite")
			db, e := OpenSQLite(path)
			if e != nil {
				t.Fatal(e)
			}
			prior := initialSchema(t, TasksDatabase)
			if skip {
				if e = migrateDatabase(t.Context(), db, TasksDatabase, prior); e != nil {
					t.Fatal(e)
				}
				execMigrationSQL(t, db, `INSERT INTO codex_link_assistants(id,name,workspace,mode,revision,thread_id) VALUES('first','One','/owner/read','read-only',8,'thread-one'),('second','Two','/owner/read','read-only',9,'thread-two'),('write','Write','/owner/write','workspace-write',3,'thread-write')`)
			} else {
				name := "migrations/tasks/0002_agent_adapter.sql"
				body, e := migrationFiles.ReadFile(name)
				if e != nil {
					t.Fatal(e)
				}
				prior[name] = &fstest.MapFile{Data: body}
				if e = migrateDatabase(t.Context(), db, TasksDatabase, prior); e != nil {
					t.Fatal(e)
				}
				execMigrationSQL(t, db, `INSERT INTO agent_adapter_assistants(id,name,workspace,mode,revision,thread_id,model) VALUES('first','One','/owner/read','read-only',8,'thread-one','model-one'),('second','Two','/owner/read','read-only',9,'thread-two','model-one'),('write','Write','/owner/write','workspace-write',3,'thread-write','model-two'); UPDATE agent_adapter_assistants SET avatar='saved-avatar' WHERE id='first'; INSERT INTO agent_adapter_management VALUES('operator','saved','{"from":{"node_id":"original","kind":"user","agent_id":"operator"},"action":"create","request_id":"saved","id":"first","name":"One","model":"model-one","workspace":"first"}','{"assistant":{"id":"first","name":"One","model":"model-one","effort":"","workspace":"first","revision":8}}')`)
			}
			if !skip {
				execMigrationSQL(t, db, `UPDATE agent_adapter_assistants SET effort='high' WHERE id='second'; INSERT INTO agent_adapter_management VALUES('operator','saved-effort','{"from":{"node_id":"original","kind":"user","agent_id":"operator"},"action":"create","request_id":"saved-effort","id":"second","name":"Two","model":"model-one","effort":"high","workspace":"first"}','{"assistant":{"id":"second","name":"Two","model":"model-one","effort":"high","workspace":"second","revision":9}}')`)
			}

			if e = Migrate(t.Context(), db, TasksDatabase); e != nil {
				t.Fatal(e)
			}
			db.Close()
			db, e = OpenDatabase(path, TasksDatabase)
			if e != nil {
				t.Fatal(e)
			}
			defer db.Close()
			var count int
			if e = db.QueryRow("SELECT COUNT(*) FROM agent_adapter_workspaces").Scan(&count); e != nil || count != 2 {
				t.Fatal(count, e)
			}
			var id string
			if e = db.QueryRow("SELECT id FROM agent_adapter_workspaces WHERE workspace='/owner/read' AND mode='read-only'").Scan(&id); e != nil || id == "first" || id == "second" {
				t.Fatal(id, e)
			}
			var thread string
			var revision int
			if e = db.QueryRow("SELECT thread_id,revision FROM agent_adapter_assistants WHERE id='first'").Scan(&thread, &revision); e != nil || thread != "thread-one" || revision != 8 {
				t.Fatal(thread, revision, e)
			}
			if !skip {
				var requestEffort, resultEffort string
				if e = db.QueryRow("SELECT json_extract(payload,'$.effort'),json_extract(result,'$.assistant.effort') FROM agent_adapter_management WHERE request_id='saved-effort'").Scan(&requestEffort, &resultEffort); e != nil || requestEffort != "high" || resultEffort != "high" {
					t.Fatal("upgrade changed optional effort", requestEffort, resultEffort, e)
				}
				var workspace, payload, avatar string
				if e = db.QueryRow("SELECT json_extract(result,'$.assistant.workspace'),json_extract(payload,'$.workspace'),json_extract(result,'$.assistant.avatar') FROM agent_adapter_management WHERE request_id='saved'").Scan(&workspace, &payload, &avatar); e != nil || workspace != id || payload != "first" || avatar != "saved-avatar" {
					t.Fatal(workspace, payload, e)
				}
			}
		})
	}
}
