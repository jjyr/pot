package storage

import (
	"context"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

func TestCodexRunProjectionUpgradePreservesAcceptedExecution(t *testing.T) {
	db := migrationDB(t)
	ctx := context.Background()
	prior := fstest.MapFS{}
	entries, _ := fs.ReadDir(migrationFiles, "migrations/control")
	for _, entry := range entries {
		if entry.Name() >= "0027_" {
			continue
		}
		path := "migrations/control/" + entry.Name()
		raw, _ := fs.ReadFile(migrationFiles, path)
		prior[path] = &fstest.MapFile{Data: raw}
	}
	if err := migrateDatabase(ctx, db, ControlDatabase, prior); err != nil {
		t.Fatal(err)
	}
	opaque := strings.Repeat("a", 64)
	_, err := db.Exec(`INSERT INTO agent_link_delegations(id,link_id,owner,request_id,payload,remote_id,body,notified) VALUES('task','link','owner','request','original-request','remote',json_object('request',json_object('executor','codex'),'info',json_object('run_id',?),'remote',json_object('run_id',?,'state','completed','result','saved-result')),0)`, opaque, opaque)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := Migrate(ctx, db, ControlDatabase); err != nil {
			t.Fatal(err)
		}
	}
	var payload, remote, run, result string
	var notified int
	var projection any
	err = db.QueryRow(`SELECT payload,remote_id,json_extract(body,'$.remote.run_id'),json_extract(body,'$.remote.result'),json_extract(body,'$.info.run_id'),notified FROM agent_link_delegations WHERE id='task'`).Scan(&payload, &remote, &run, &result, &projection, &notified)
	if err != nil || payload != "original-request" || remote != "remote" || run != opaque || result != "saved-result" || projection != nil || notified != 0 {
		t.Fatal(payload, remote, run, result, projection, notified, err)
	}
}
