package storage

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestTaskSubmissionUpgradePreservesAcceptedExecutionAndDelivery(t *testing.T) {
	root := t.TempDir()
	source, err := OpenSQLite(filepath.Join(root, "control.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	nodeMigrationPrefix(t, source, ControlDatabase, 28)
	target, err := OpenSQLite(filepath.Join(root, "tasks.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	nodeMigrationPrefix(t, target, TasksDatabase, 23)
	execMigrationSQL(t, target, `INSERT INTO tasks VALUES('owner','local','running',1,'{"schema_version":11,"run_id":"original-local-run","launch":{"control_id":"original-node"}}'); INSERT INTO task_writes VALUES('owner','local-request','original-digest','local')`)
	execMigrationSQL(t, target, `INSERT INTO task_outcomes(assistant_id,task_id,title,phase,updated_at_ms)VALUES('owner','linked','Linked task','complete',2)`)
	payload := `{"request_id":"request","text":"work","executor":"codex","link_id":"connection"}`
	body := `{"info":{"id":"linked","assistant_id":"owner","phase":"complete","run_id":"original-run","created_at_ms":2,"agent_link":{"link_id":"connection","node_id":"codex","kind":"codex"}},"request":{"request_id":"request","text":"work","executor":"codex","from":{"node_id":"node","kind":"assistant","agent_id":"owner"}},"remote":{"id":"original-thread","request_id":"request","run_id":"original-turn","state":"completed","result":"retained"}}`
	if _, err = source.Exec("INSERT INTO agent_link_delegations(id,link_id,owner,request_id,payload,remote_id,body,notified,stop_requested)VALUES('linked','connection','owner','request',?,'original-thread',?,1,1)", payload, body); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = ImportTaskSubmissions(root); err != nil {
			t.Fatal(err)
		}
	}
	var raw string
	if err = target.QueryRow("SELECT body FROM tasks WHERE id='linked'").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var record struct {
		SchemaVersion int    `json:"schema_version"`
		RunID         string `json:"run_id"`
	}

	var doc map[string]any
	if err = json.Unmarshal([]byte(raw), &record); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatal(err)
	}
	execution := doc["execution"].(map[string]any)
	receipt := execution["receipt"].(map[string]any)
	if record.SchemaVersion != 12 || record.RunID != "original-run" || receipt["id"] != "original-thread" || receipt["run_id"] != "original-turn" || receipt["state"] != "completed" || execution["notified"] != true || execution["stop_requested"] != true {
		t.Fatal(raw)
	}
	if doc["result"].(map[string]any)["text"] != "retained" || doc["outcome"].(map[string]any)["text"] != "retained" {
		t.Fatal("lost completed result", raw)
	}
	var outcomeRun string
	if err = target.QueryRow("SELECT run_id FROM task_outcomes WHERE task_id='linked'").Scan(&outcomeRun); err != nil || outcomeRun != "original-run" {
		t.Fatal("lost outcome receipt", outcomeRun, err)
	}
	var digest, run string
	if err = target.QueryRow("SELECT digest FROM task_writes WHERE request_id='local-request'").Scan(&digest); err != nil || digest != "original-digest" {
		t.Fatal(digest, err)
	}
	if err = target.QueryRow("SELECT json_extract(body,'$.run_id') FROM tasks WHERE id='local'").Scan(&run); err != nil || run != "original-local-run" {
		t.Fatal(run, err)
	}
	var events int
	if err = target.QueryRow("SELECT count(*) FROM agent_messages").Scan(&events); err != nil || events != 0 {
		t.Fatal("migration emitted work", events, err)
	}
	// Once imported, the archived source can never recreate a removed live task.
	execMigrationSQL(t, target, "DELETE FROM tasks WHERE id='linked'")
	if err = ImportTaskSubmissions(root); err != nil {
		t.Fatal(err)
	}
	if err = target.QueryRow("SELECT count(*) FROM tasks WHERE id='linked'").Scan(&events); err != nil || events != 0 {
		t.Fatal("reimported archived work", events, err)
	}
}

func TestTaskSubmissionUpgradeConflictRollsBackAndCanRetry(t *testing.T) {
	root := t.TempDir()
	source, err := OpenDatabase(filepath.Join(root, "control.sqlite"), ControlDatabase)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	target, err := OpenDatabase(filepath.Join(root, "tasks.sqlite"), TasksDatabase)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	body := `{"info":{"id":"task","assistant_id":"owner","phase":"queued","agent_link":{"link_id":"peer"}},"request":{"request_id":"original","text":"work"},"remote":{}}`
	if _, err = source.Exec("INSERT INTO retired_task_submissions(id,link_id,owner,request_id,payload,body)VALUES('task','peer','owner','original','{\"request_id\":\"original\",\"text\":\"work\"}',?)", body); err != nil {
		t.Fatal(err)
	}
	execMigrationSQL(t, target, `INSERT INTO tasks VALUES('owner','task','queued',1,'{"untouched":true}')`)
	if err = ImportTaskSubmissions(root); err == nil {
		t.Fatal("overwrote conflicting task")
	}
	var count int
	if err = target.QueryRow("SELECT count(*) FROM task_submission_import").Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	execMigrationSQL(t, target, "DELETE FROM tasks WHERE id='task'")
	if err = ImportTaskSubmissions(root); err != nil {
		t.Fatal(err)
	}
	if err = Migrate(context.Background(), target, TasksDatabase); err != nil {
		t.Fatal(err)
	}
}
