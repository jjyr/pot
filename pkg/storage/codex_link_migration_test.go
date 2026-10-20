package storage

import (
	"context"
	"io/fs"
	"testing"
	"testing/fstest"
)

func TestCodexLinkUpgradePreservesMailAndDoesNotLaunchWork(t *testing.T) {
	db := migrationDB(t)
	old := fstest.MapFS{}
	entries, err := fs.ReadDir(migrationFiles, "migrations/tasks")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() >= "0009_" {
			continue
		}
		name := "migrations/tasks/" + entry.Name()
		data, err := fs.ReadFile(migrationFiles, name)
		if err != nil {
			t.Fatal(err)
		}
		old[name] = &fstest.MapFile{Data: data}
	}
	ctx := context.Background()
	if err = migrateDatabase(ctx, db, TasksDatabase, old); err != nil {
		t.Fatal(err)
	}
	// The old durable inbox and consumer claims stay intact when virtual session
	// storage is added. Migration must never manufacture a task or start a model.
	execMigrationSQL(t, db, `INSERT INTO codex_mail_consumers(id,parent_id,active) VALUES('old-reader',NULL,0)`)
	for range 2 {
		if err = Migrate(ctx, db, TasksDatabase); err != nil {
			t.Fatal(err)
		}
		var active, count int
		if err = db.QueryRow("SELECT active FROM codex_mail_consumers WHERE id='old-reader'").Scan(&active); err != nil || active != 0 {
			t.Fatal("claim changed", active, err)
		}
		for _, table := range []string{"codex_link_jobs", "codex_link_assistants"} {
			if err = db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != 0 {
				t.Fatal("migration manufactured work", table, count, err)
			}
		}
	}
}
