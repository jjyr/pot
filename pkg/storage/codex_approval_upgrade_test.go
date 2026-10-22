package storage

import (
	"context"
	"io/fs"
	"testing"
	"testing/fstest"
)

func TestCodexTaskUpgradeRetainsHistoryWithoutSharedSessionAuthority(t *testing.T) {
	db := migrationDB(t)
	prior := fstest.MapFS{}
	entries, _ := fs.ReadDir(migrationFiles, "migrations/tasks")
	for _, entry := range entries {
		if entry.Name() >= "0020_" {
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
	execMigrationSQL(t, db, `INSERT INTO codex_link_assistants(id,name,workspace,mode,thread_id,is_default) VALUES('codex','Saved','/work','read-only','original-thread',1)`)
	execMigrationSQL(t, db, `INSERT INTO codex_approval_peers VALUES('codex','formerly-trusted')`)
	execMigrationSQL(t, db, `INSERT INTO codex_link_jobs(id,caller,request_id,assistant_id,payload,data) VALUES('job','caller','request','codex','immutable-payload','{"task":{"state":"completed"},"thread_id":"original-thread"}')`)
	for range 2 {
		if err := Migrate(ctx, db, TasksDatabase); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='codex_approval_peers'").Scan(&count); err != nil || count != 0 {
		t.Fatal("obsolete approval grant retained", count, err)
	}
	if err := db.QueryRow("SELECT count(*) FROM codex_task_sessions").Scan(&count); err != nil || count != 0 {
		t.Fatal("shared assistant session assigned to a task", count, err)
	}
	var thread, mode, payload, data string
	if err := db.QueryRow("SELECT thread_id,mode FROM codex_link_assistants WHERE id='codex'").Scan(&thread, &mode); err != nil || thread != "original-thread" || mode != "read-only" {
		t.Fatal(thread, mode, err)
	}
	if err := db.QueryRow("SELECT payload,data FROM codex_link_jobs WHERE id='job'").Scan(&payload, &data); err != nil || payload != "immutable-payload" || data != `{"task":{"state":"completed"},"thread_id":"original-thread"}` {
		t.Fatal(payload, data, err)
	}
}
