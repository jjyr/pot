package storage

import (
	"io/fs"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestMCPTaskHumanUpgradeNeverInfersAuthority(t *testing.T) {
	for _, start := range []string{"0009", "0010"} {
		t.Run(start, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "tasks.sqlite")
			db, err := OpenSQLite(path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			prior := fstest.MapFS{}
			entries, err := fs.ReadDir(migrationFiles, "migrations/tasks")
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if entry.Name()[:4] <= start {
					name := "migrations/tasks/" + entry.Name()
					raw, err := fs.ReadFile(migrationFiles, name)
					if err != nil {
						t.Fatal(err)
					}
					prior[name] = &fstest.MapFile{Data: raw}
				}
			}
			if err = migrateDatabase(t.Context(), db, TasksDatabase, prior); err != nil {
				t.Fatal(err)
			}
			receipt := `{"task":{"id":"original-dispatch","run_id":"original-run"}}`
			if _, err = db.Exec("INSERT INTO mcp_local_mutations VALUES(?,?,?,?,?,?,?,?)", "connection", "original-actor", "task.submit", "original-request", "original-digest", "{}", "accepted", receipt); err != nil {
				t.Fatal(err)
			}
			if err = Migrate(t.Context(), db, TasksDatabase); err != nil {
				t.Fatal(err)
			}
			var count int
			if err = db.QueryRow("SELECT count(*) FROM mcp_task_humans").Scan(&count); err != nil || count != 0 {
				t.Fatal("upgrade inferred human authority", count, err)
			}
			var saved string
			if err = db.QueryRow("SELECT receipt FROM mcp_local_mutations WHERE request_id='original-request'").Scan(&saved); err != nil || saved != receipt {
				t.Fatal("historical receipt changed", saved, err)
			}
			if _, err = db.Exec("INSERT INTO mcp_task_humans VALUES('connection','actor','request','original-human')"); err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec("INSERT INTO mcp_task_humans VALUES('connection','actor','request','other-human')"); err == nil {
				t.Fatal("origin overwrite permitted")
			}
			if err = Migrate(t.Context(), db, TasksDatabase); err != nil {
				t.Fatal(err)
			}
			if err = db.Close(); err != nil {
				t.Fatal(err)
			}
			db, err = OpenDatabase(path, TasksDatabase)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if err = db.QueryRow("SELECT human FROM mcp_task_humans WHERE request_id='request'").Scan(&saved); err != nil || saved != "original-human" {
				t.Fatal(saved, err)
			}
		})
	}
}
