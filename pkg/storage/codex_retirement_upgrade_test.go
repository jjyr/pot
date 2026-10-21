package storage

import (
	"context"
	"io/fs"
	"testing"
	"testing/fstest"
)

func TestCodexRetirementUpgradePreservesSessionsAndHistory(t *testing.T) {
	db := migrationDB(t)
	prior := fstest.MapFS{}
	entries, _ := fs.ReadDir(migrationFiles, "migrations/tasks")
	for _, entry := range entries {
		if entry.Name() >= "0018_" {
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
	execMigrationSQL(t, db, `INSERT INTO codex_link_assistants(id,name,workspace,mode,thread_id,is_default,revision) VALUES('codex','Saved','/work','read-only','original',1,7)`)
	execMigrationSQL(t, db, `INSERT INTO codex_link_jobs(id,caller,request_id,assistant_id,payload,data) VALUES('job','caller','request','codex','immutable','{"task":{"state":"completed"}}')`)
	if err := Migrate(ctx, db, TasksDatabase); err != nil {
		t.Fatal(err)
	}
	var deleted bool
	var thread string
	var rev int
	if err := db.QueryRow("SELECT deleted,thread_id,revision FROM codex_link_assistants").Scan(&deleted, &thread, &rev); err != nil || deleted || thread != "original" || rev != 7 {
		t.Fatal(deleted, thread, rev, err)
	}
	execMigrationSQL(t, db, "UPDATE codex_link_assistants SET deleted=1,is_default=0")
	if err := Migrate(ctx, db, TasksDatabase); err != nil {
		t.Fatal(err)
	}
	var payload string
	if err := db.QueryRow("SELECT payload FROM codex_link_jobs").Scan(&payload); err != nil || payload != "immutable" {
		t.Fatal(payload, err)
	}
	if err := db.QueryRow("SELECT deleted FROM codex_link_assistants").Scan(&deleted); err != nil || !deleted {
		t.Fatal("retirement lost", err)
	}
}
