package storage

import (
	"context"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestAgentLinkTaskUpgradePreservesRunsEffectsAndReceipts(t *testing.T) {
	ctx := context.Background()
	db, e := OpenSQLite(filepath.Join(t.TempDir(), "tasks.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	old := fstest.MapFS{}
	e = fs.WalkDir(migrationFiles, "migrations/tasks", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && d.Name() < "0011_" {
			data, e := migrationFiles.ReadFile(path)
			if e != nil {
				return e
			}
			old[path] = &fstest.MapFile{Data: data}
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	if e = migrateDatabase(ctx, db, TasksDatabase, old); e != nil {
		t.Fatal(e)
	}
	body := `{"schema_version":10,"assistant_id":"main","run_id":"original-run","launch_sent":true,"request":{"request_id":"original","text":"work"},"info":{"id":"task","phase":"interrupted"},"error":"effects_unknown"}`
	if _, e = db.Exec("INSERT INTO tasks VALUES('main','task','interrupted',1,?); INSERT INTO task_run_history VALUES('main','task','original-run',?); INSERT INTO task_writes VALUES('main','original','original-digest','task');", body, body); e != nil {
		t.Fatal(e)
	}
	for range 2 {
		if e = Migrate(ctx, db, TasksDatabase); e != nil {
			t.Fatal(e)
		}
	}
	for _, table := range []string{"tasks", "task_run_history"} {
		var raw string
		if e = db.QueryRow("SELECT body FROM " + table).Scan(&raw); e != nil {
			t.Fatal(e)
		}
		for _, fact := range []string{`"run_id":"original-run"`, `"schema_version":13`, `"phase":"interrupted"`, `"effects_unknown"`, `"agent_link":null`} {
			if !strings.Contains(raw, fact) {
				t.Fatal(table, fact, raw)
			}
		}
	}
	var digest, id string
	if e = db.QueryRow("SELECT digest,task_id FROM task_writes").Scan(&digest, &id); e != nil || digest != "original-digest" || id != "task" {
		t.Fatal(digest, id, e)
	}
}
