package storage

import (
	"context"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

func TestCodexConsumerUpgradeFencesPreviouslyReadMessages(t *testing.T) {
	ctx := context.Background()
	db := migrationDB(t)
	prior := fstest.MapFS{}
	if err := fs.WalkDir(migrationFiles, "migrations/tasks", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && !strings.HasSuffix(path, "0005_codex_consumers.sql") {
			raw, e := fs.ReadFile(migrationFiles, path)
			if e != nil {
				return e
			}
			prior[path] = &fstest.MapFile{Data: raw}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := migrateDatabase(ctx, db, TasksDatabase, prior); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"queued", "read", "processed"} {
		if _, err := db.Exec("INSERT INTO agent_messages(id,envelope,direction,state,conversation,recipient)VALUES(?,?,'incoming',?,'conversation','codex')", state, `{"id":"`+state+`"}`, state); err != nil {
			t.Fatal(err)
		}
	}
	if err := Migrate(ctx, db, TasksDatabase); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, db, TasksDatabase); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]string{"queued": "queued", "read": "interrupted", "processed": "processed"} {
		var state, body string
		if err := db.QueryRow("SELECT state,envelope FROM agent_messages WHERE id=?", id).Scan(&state, &body); err != nil || state != want || body != `{"id":"`+id+`"}` {
			t.Fatal(id, state, body, err)
		}
	}
}
