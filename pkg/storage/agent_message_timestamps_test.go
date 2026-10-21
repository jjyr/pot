package storage

import (
	"context"
	"io/fs"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestMessageTimestampUpgradePreservesUnknownHistory(t *testing.T) {
	ctx := context.Background()
	db, err := OpenSQLite(filepath.Join(t.TempDir(), "tasks.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	old := fstest.MapFS{}
	err = fs.WalkDir(migrationFiles, "migrations/tasks", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && d.Name() < "0012_" {
			data, e := migrationFiles.ReadFile(path)
			if e != nil {
				return e
			}
			old[path] = &fstest.MapFile{Data: data}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = migrateDatabase(ctx, db, TasksDatabase, old); err != nil {
		t.Fatal(err)
	}
	const envelope = `{"id":"historical","text":"immutable"}`
	if _, err = db.Exec("INSERT INTO agent_messages(id,envelope,direction,state,conversation,recipient)VALUES('historical',?,'out','queued','conversation','main')", envelope); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = Migrate(ctx, db, TasksDatabase); err != nil {
			t.Fatal(err)
		}
	}
	var raw, state string
	var at int64
	if err = db.QueryRow("SELECT envelope,state,accepted_at_ms FROM agent_messages WHERE id='historical'").Scan(&raw, &state, &at); err != nil || raw != envelope || state != "queued" || at != 0 {
		t.Fatal(raw, state, at, err)
	}
}
