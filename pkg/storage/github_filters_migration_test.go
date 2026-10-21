package storage

import (
	"context"
	"encoding/json"
	"io/fs"
	"testing"
	"testing/fstest"
)

func TestGitHubFiltersUpgrade(t *testing.T) {
	for _, config := range []string{`{}`, `{"repositories":[],"reasons":[]}`, `{"repositories":["me/repo"],"reasons":["subscribed"]}`} {
		t.Run(config, func(t *testing.T) {
			db := migrationDB(t)
			ctx := context.Background()
			old := fstest.MapFS{}
			entries, _ := fs.ReadDir(migrationFiles, "migrations/tasks")
			for _, entry := range entries {
				if entry.Name() >= "0014_" {
					continue
				}
				path := "migrations/tasks/" + entry.Name()
				raw, _ := fs.ReadFile(migrationFiles, path)
				old[path] = &fstest.MapFile{Data: raw}
			}
			if err := migrateDatabase(ctx, db, TasksDatabase, old); err != nil {
				t.Fatal(err)
			}
			state := `{"account":"123","baseline":true,"since":"2026-01-01T00:00:00Z","threads":{"1":{"marker":"keep"}},"pending":{"old":{}},"scan":{"rows":[1]}}`
			_, err := db.Exec("INSERT INTO plugin_background(id,plugin_id,connection_id,enabled,assistant,config,revision,state,next,status,conversation) VALUES('github','github','',0,'main',?,7,?,123,'ready','conversation')", config, state)
			if err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if err = Migrate(ctx, db, TasksDatabase); err != nil {
					t.Fatal(err)
				}
			}
			var raw, checkpoint string
			var enabled bool
			var revision, next int
			if err = db.QueryRow("SELECT config,state,enabled,revision,next FROM plugin_background WHERE id='github'").Scan(&raw, &checkpoint, &enabled, &revision, &next); err != nil {
				t.Fatal(err)
			}
			var filters map[string]any
			json.Unmarshal([]byte(raw), &filters)
			reasons, ok := filters["reasons"].([]any)
			if !ok {
				t.Fatal("array quoted by migration", raw)
			}
			want := "mention"
			if config == `{"repositories":["me/repo"],"reasons":["subscribed"]}` {
				want = "subscribed"
				if filters["repositories"].([]any)[0] != "me/repo" {
					t.Fatal(raw)
				}
			} else if filters["repositories"] != nil {
				t.Fatal(raw)
			}
			if reasons[0] != want || filters["account_id"] != "123" || enabled || revision != 8 || next != 123 {
				t.Fatal(raw, enabled, revision, next)
			}
			var progress map[string]any
			json.Unmarshal([]byte(checkpoint), &progress)
			if progress["since"] != "2026-01-01T00:00:00Z" || progress["threads"].(map[string]any)["1"].(map[string]any)["marker"] != "keep" || len(progress["pending"].(map[string]any)) != 0 || progress["scan"] != nil {
				t.Fatal(checkpoint)
			}
		})
	}
}
