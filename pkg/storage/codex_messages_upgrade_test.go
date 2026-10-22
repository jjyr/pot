package storage

import (
	"context"
	"encoding/json"
	"io/fs"
	"testing"
	"testing/fstest"
)

func TestCodexMessageUpgradeSettlesUserChatsWithoutReplaying(t *testing.T) {
	db := migrationDB(t)
	prior := fstest.MapFS{}
	entries, _ := fs.ReadDir(migrationFiles, "migrations/tasks")
	for _, entry := range entries {
		if entry.Name() >= "0021_" {
			continue
		}
		path := "migrations/tasks/" + entry.Name()
		raw, _ := fs.ReadFile(migrationFiles, path)
		prior[path] = &fstest.MapFile{Data: raw}
	}
	ctx := context.Background()
	if err := migrateDatabase(ctx, db, TasksDatabase, prior); err != nil {
		t.Fatal(err)
	}
	execMigrationSQL(t, db, `INSERT INTO codex_link_assistants(id,name,workspace,mode,thread_id,is_default) VALUES('codex','Saved','/work','read-only','message-thread',1)`)
	type fixture struct{ id, kind, state string }
	cases := []fixture{{"queued-user", "user", "queued"}, {"read-user", "user", "read"}, {"completed-user", "user", "processed"}, {"assistant", "assistant", "queued"}, {"task", "task", "queued"}}
	envelopes, jobs := map[string]string{}, map[string]string{}
	for _, c := range cases {
		envelope, _ := json.Marshal(map[string]any{"id": c.id, "kind": "message", "from": map[string]string{"kind": c.kind}, "text": "saved input"})
		envelopes[c.id] = string(envelope)
		if _, err := db.Exec(`INSERT INTO agent_messages(id,envelope,direction,state,conversation,recipient,detail) VALUES(?,?,'incoming',?,'conversation','codex','original')`, c.id, envelope, c.state); err != nil {
			t.Fatal(err)
		}
		state := "queued"
		if c.state == "processed" {
			state = "completed"
		}
		data, _ := json.Marshal(map[string]any{"task": map[string]string{"state": state, "result": "retained result"}, "message": json.RawMessage(envelope), "thread_id": "message-thread"})
		jobs[c.id] = string(data)
		if _, err := db.Exec(`INSERT INTO codex_link_jobs(id,caller,request_id,assistant_id,payload,data) VALUES(?,'caller',?,'codex','immutable-payload',?)`, c.id, c.id, data); err != nil {
			t.Fatal(err)
		}
	}
	for _, direction := range []string{"in", "outgoing"} {
		if _, err := db.Exec(`INSERT INTO agent_messages(id,envelope,direction,state,conversation,recipient,detail) VALUES(?, ?, ?, 'queued','conversation','codex','original')`, direction, envelopes["queued-user"], direction); err != nil {
			t.Fatal(err)
		}
	}
	// A direct Codex task has no message parent, even though its caller is an
	// assistant. Its independent persistent session must remain untouched.
	execMigrationSQL(t, db, `INSERT INTO codex_link_jobs(id,caller,request_id,payload,data) VALUES('direct','caller','direct','immutable-task','{"task":{"state":"queued"},"thread_id":"task-thread"}')`)
	for range 2 {
		if err := Migrate(ctx, db, TasksDatabase); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range cases {
		var envelope, state, detail, data, payload string
		if err := db.QueryRow("SELECT envelope,state,detail FROM agent_messages WHERE id=?", c.id).Scan(&envelope, &state, &detail); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow("SELECT data,payload FROM codex_link_jobs WHERE id=?", c.id).Scan(&data, &payload); err != nil {
			t.Fatal(err)
		}
		if envelope != envelopes[c.id] || payload != "immutable-payload" {
			t.Fatal("migration changed immutable request", c.id)
		}
		if c.kind == "user" && c.state != "processed" {
			var job struct {
				Task   struct{ State, Error, Result string }
				Thread string `json:"thread_id"`
			}
			if err := json.Unmarshal([]byte(data), &job); err != nil {
				t.Fatal(err)
			}
			if state != "processed" || detail != "agent_link_capability_unsupported" || job.Task.State != "failed" || job.Task.Error != detail || job.Task.Result != "retained result" || job.Thread != "message-thread" {
				t.Fatal("pending user chat remained executable", c.id, state, detail, data)
			}
		} else if state != c.state || detail != "original" || data != jobs[c.id] {
			t.Fatal("migration changed retained work", c.id, state, detail, data)
		}
	}
	for _, direction := range []string{"in", "outgoing"} {
		var envelope, state, detail string
		if err := db.QueryRow("SELECT envelope,state,detail FROM agent_messages WHERE id=?", direction).Scan(&envelope, &state, &detail); err != nil || envelope != envelopes["queued-user"] || state != "queued" || detail != "original" {
			t.Fatal("non-Codex inbox changed", direction, envelope, state, detail, err)
		}
	}
	var direct, thread string
	if err := db.QueryRow("SELECT data FROM codex_link_jobs WHERE id='direct'").Scan(&direct); err != nil || direct != `{"task":{"state":"queued"},"thread_id":"task-thread"}` {
		t.Fatal("direct task changed", direct, err)
	}
	if err := db.QueryRow("SELECT thread_id FROM codex_link_assistants WHERE id='codex'").Scan(&thread); err != nil || thread != "message-thread" {
		t.Fatal("assistant thread changed", thread, err)
	}
}
