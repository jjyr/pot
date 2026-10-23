package storage

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

func TestGenericExecutorUpgradePreservesTaskReceipts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.sqlite")
	db, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	nodeMigrationPrefix(t, db, TasksDatabase, 24)
	request := legacyCreateTask{Executor: "codex", RequestID: "request", Text: "任务 <tag> & \"quoted\"\n继续", LinkID: "connection"}
	origin := protocol.TaskOrigin{Kind: "manual"}
	body := map[string]any{
		"schema_version": 12, "request": request, "origin": origin,
		"info":      map[string]any{"id": "task", "phase": "waiting", "run_id": "run", "archived_at_ms": 123},
		"execution": map[string]any{"request": map[string]any{"executor": "codex", "request_id": "request", "text": request.Text}, "receipt": map[string]any{"id": "receiver-task", "run_id": "receiver-run", "state": "cancelling"}, "notified": true, "stop_requested": true},
	}
	raw, _ := json.Marshal(body)
	oldDigest, _ := core.HashJSON([]any{request, origin})
	if _, err = db.Exec("INSERT INTO tasks VALUES('owner','task','waiting',1,?)", string(raw)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO task_run_history VALUES('owner','task','run',?)", string(raw)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO task_writes VALUES('owner','request',?,'task')", oldDigest); err != nil {
		t.Fatal(err)
	}
	execMigrationSQL(t, db, `INSERT INTO tasks VALUES('owner','local','running',1,'{"request":{"request_id":"local"}}'); INSERT INTO task_writes VALUES('owner','local','unchanged','local')`)
	for range 2 {
		if err = migrateThrough(db, TasksDatabase, 25); err != nil {
			t.Fatal(err)
		}
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	request.Executor = "agent"
	var expected map[string]any
	if err = json.Unmarshal(raw, &expected); err != nil {
		t.Fatal(err)
	}
	expected["request"].(map[string]any)["executor"] = "agent"
	expected["execution"].(map[string]any)["request"].(map[string]any)["executor"] = "agent"
	for _, query := range []string{"SELECT body FROM tasks WHERE id='task'", "SELECT body FROM task_run_history WHERE task_id='task'"} {
		var stored string
		if err = db.QueryRow(query).Scan(&stored); err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		if err = json.Unmarshal([]byte(stored), &got); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, expected) {
			t.Fatalf("receipt changed: %s", stored)
		}
	}
	var digest string
	expectedDigest, _ := core.HashJSON([]any{request, origin})
	if err = db.QueryRow("SELECT digest FROM task_writes WHERE request_id='request'").Scan(&digest); err != nil || digest != expectedDigest {
		t.Fatal(digest, expectedDigest, err)
	}
	if err = db.QueryRow("SELECT digest FROM task_writes WHERE request_id='local'").Scan(&digest); err != nil || digest != "unchanged" {
		t.Fatal(digest, err)
	}
	var count int
	if err = db.QueryRow("SELECT count(*) FROM agent_messages").Scan(&count); err != nil || count != 0 {
		t.Fatal("migration emitted work", count, err)
	}
}

func TestGenericExecutorUpgradeRollsBackRequestBindingsAndRetries(t *testing.T) {
	db, err := OpenSQLite(filepath.Join(t.TempDir(), "tasks.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	nodeMigrationPrefix(t, db, TasksDatabase, 24)
	original := `{"request":{"executor":"codex","request_id":"request","text":"work"},"origin":{"kind":"manual"}}`
	if _, err = db.Exec("INSERT INTO tasks VALUES('owner','task','queued',1,?)", original); err != nil {
		t.Fatal(err)
	}
	execMigrationSQL(t, db, `INSERT INTO task_writes VALUES('owner','request','original-digest','task'); INSERT INTO codex_link_jobs(id,caller,request_id,payload,data) VALUES('job','caller','request','{"executor":"codex","from":"corrupt"}','{"request":{"executor":"codex"},"cleanup_uncertain":true}')`)
	if err = migrateThrough(db, TasksDatabase, 25); err == nil {
		t.Fatal("corrupt immutable payload accepted")
	}
	var body, digest string
	if err = db.QueryRow("SELECT body FROM tasks WHERE id='task'").Scan(&body); err != nil || body != original {
		t.Fatal("partially converted request", body, err)
	}
	if err = db.QueryRow("SELECT digest FROM task_writes WHERE task_id='task'").Scan(&digest); err != nil || digest != "original-digest" {
		t.Fatal("partially converted digest", digest, err)
	}
	var receipts int
	if err = db.QueryRow("SELECT count(*) FROM pot_migrations WHERE id LIKE '0025_%'").Scan(&receipts); err != nil || receipts != 0 {
		t.Fatal("committed failed migration", receipts, err)
	}
	execMigrationSQL(t, db, `UPDATE codex_link_jobs SET payload='{"executor":"codex","request_id":"request","from":{"node_id":"caller","kind":"assistant","agent_id":"owner"},"text":"work"}' WHERE id='job'`)
	if err = migrateThrough(db, TasksDatabase, 25); err != nil {
		t.Fatal(err)
	}
	var selector string
	if err = db.QueryRow("SELECT json_extract(body,'$.request.executor') FROM tasks WHERE id='task'").Scan(&selector); err != nil || selector != "agent" {
		t.Fatal(selector, err)
	}
}
