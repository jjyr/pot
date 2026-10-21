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
			var collection struct {
				Initialized   bool
				Subscriptions []struct {
					ID, Assistant string
					AccountID     string `json:"account_id"`
					Enabled       bool
					Revision      int
					Filters       map[string]any
				}
			}
			if err = json.Unmarshal([]byte(raw), &collection); err != nil || !collection.Initialized || len(collection.Subscriptions) != 1 {
				t.Fatal(raw, err)
			}
			sub := collection.Subscriptions[0]
			if sub.ID != "legacy-github" || sub.Assistant != "main" || sub.Enabled || sub.Revision != 1 {
				t.Fatal(raw)
			}
			filters := sub.Filters
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
			if reasons[0] != want || sub.AccountID != "123" || !enabled || revision != 9 || next != 123 {
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

func TestGitHubSubscriptionUpgradeFromPublishedFilters(t *testing.T) {
	ctx := context.Background()
	db := migrationDB(t)
	previous := fstest.MapFS{}
	entries, _ := fs.ReadDir(migrationFiles, "migrations/tasks")
	for _, entry := range entries {
		if entry.Name() >= "0015_" {
			continue
		}
		path := "migrations/tasks/" + entry.Name()
		raw, _ := fs.ReadFile(migrationFiles, path)
		previous[path] = &fstest.MapFile{Data: raw}
	}
	if err := migrateDatabase(ctx, db, TasksDatabase, previous); err != nil {
		t.Fatal(err)
	}
	config := `{"account_id":"123","repositories":["me/one","me/two"],"reasons":[],"subject_types":["Release"],"threads":["42"]}`
	state := `{"account":"123","baseline":true,"since":"2026-01-01T00:00:00Z","filterSince":"2026-01-01T00:01:00Z","threads":{"42":{"marker":"keep"}},"pending":{"old":{}},"scan":{"rows":[1]}}`
	if _, err := db.Exec(`INSERT INTO plugin_background(id,plugin_id,connection_id,enabled,assistant,config,revision,state,next,status,conversation) VALUES('github','github','',0,'B',?,7,?,123,'ready','conversation')`, config, state); err != nil {
		t.Fatal(err)
	}
	execMigrationSQL(t, db, `INSERT INTO plugin_background_writes VALUES('github','old-save','old-hash')`)
	for range 2 {
		if err := Migrate(ctx, db, TasksDatabase); err != nil {
			t.Fatal(err)
		}
	}
	var raw, assistant, checkpoint, receipt string
	var enabled bool
	var revision int
	if err := db.QueryRow(`SELECT config,assistant,enabled,revision,state FROM plugin_background WHERE id='github'`).Scan(&raw, &assistant, &enabled, &revision, &checkpoint); err != nil {
		t.Fatal(err)
	}
	var collection struct {
		Initialized   bool
		Subscriptions []struct {
			Assistant string
			Enabled   bool
			Revision  int
			ChangedAt string `json:"changed_at"`
			AccountID string `json:"account_id"`
			Filters   json.RawMessage
		}
	}
	if err := json.Unmarshal([]byte(raw), &collection); err != nil {
		t.Fatal(err)
	}
	if !collection.Initialized || len(collection.Subscriptions) != 1 || assistant != "" || !enabled || revision != 8 {
		t.Fatal(raw, assistant, enabled, revision)
	}
	sub := collection.Subscriptions[0]
	var filters map[string]any
	json.Unmarshal(sub.Filters, &filters)
	if sub.Assistant != "B" || sub.Enabled || sub.Revision != 1 || sub.AccountID != "123" || sub.ChangedAt != "2026-01-01T00:01:00Z" || len(filters["reasons"].([]any)) != 0 || len(filters["repositories"].([]any)) != 2 || filters["threads"].([]any)[0] != "42" || filters["subject_types"].([]any)[0] != "Release" {
		t.Fatal(raw)
	}
	var progress map[string]any
	json.Unmarshal([]byte(checkpoint), &progress)
	if progress["since"] != "2026-01-01T00:00:00Z" || progress["threads"].(map[string]any)["42"].(map[string]any)["marker"] != "keep" || len(progress["pending"].(map[string]any)) != 0 || progress["scan"] != nil {
		t.Fatal(checkpoint)
	}
	if err := db.QueryRow(`SELECT input_hash FROM plugin_background_writes WHERE request_id='old-save'`).Scan(&receipt); err != nil || receipt != "old-hash" {
		t.Fatal(receipt, err)
	}
}
