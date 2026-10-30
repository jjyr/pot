package storage

import (
	"context"
	"path/filepath"
	"testing"
)

func TestTaskParametersMigration(t *testing.T) {
	for _, kind := range []Database{ConversationDatabase, ControlDatabase} {
		for _, skipped := range []bool{false, true} {
			t.Run(string(kind)+map[bool]string{false: "/preceding", true: "/skipped"}[skipped], func(t *testing.T) {
				ctx := context.Background()
				path := filepath.Join(t.TempDir(), "data.sqlite")
				db, err := OpenSQLite(path)
				if err != nil {
					t.Fatal(err)
				}
				paths := taskDefaultMigrationPaths(t, kind)
				prior := paths[:len(paths)-1]
				if skipped {
					prior = prior[:1]
				}
				migrateTaskDefaultPaths(t, db, kind, prior)
				old := `{"task_default":{"profile_id":"p","model":"m","context_window":64000},"other":7}`
				var query string
				if kind == ConversationDatabase {
					_, err = db.ExecContext(ctx, "INSERT INTO metadata VALUES(?,?)", "assistant.configuration.v1", `{"revision":3,"intent":`+old+`,"pending_spec":{"model":"keep"}}`)
					query = "SELECT json_extract(value,'$.intent.task_default.context_window'),json_type(value,'$.intent.task_default.max_output_tokens'),json_type(value,'$.intent.task_default.reasoning_effort'),json_extract(value,'$.revision'),json_extract(value,'$.pending_spec.model') FROM metadata WHERE key='assistant.configuration.v1'"
					if !skipped {
						insertTaskDefaultReceipt(t, ctx, db, "request", "hash", `{"intent":`+old+`}`)
					}
				} else {
					_, err = db.ExecContext(ctx, "INSERT INTO assistant_initial_intent VALUES(?,?)", "owner", old)
					query = "SELECT json_extract(intent,'$.task_default.context_window'),json_type(intent,'$.task_default.max_output_tokens'),json_type(intent,'$.task_default.reasoning_effort'),json_extract(intent,'$.other'),'keep' FROM assistant_initial_intent WHERE id='owner'"
				}
				if err != nil {
					t.Fatal(err)
				}
				for i := 0; i < 2; i++ {
					if err = Migrate(ctx, db, kind); err != nil {
						t.Fatal(err)
					}
					var window, counter int
					var output, effort, pending string
					if err = db.QueryRowContext(ctx, query).Scan(&window, &output, &effort, &counter, &pending); err != nil {
						t.Fatal(err)
					}
					want := 7
					if kind == ConversationDatabase {
						want = 3
					}
					if window != 64000 || output != "null" || effort != "null" || counter != want || pending != "keep" {
						t.Fatalf("unexpected upgraded values: %d %s %s %d %s", window, output, effort, counter, pending)
					}
					if kind == ConversationDatabase && !skipped {
						var hash, typ string
						if err = db.QueryRowContext(ctx, "SELECT input_hash,json_type(response,'$.intent.task_default.max_output_tokens') FROM assistant_configuration_requests WHERE id='request'").Scan(&hash, &typ); err != nil {
							t.Fatal(err)
						}
						if hash != "hash" || typ != "null" {
							t.Fatal("receipt changed")
						}
					}
					if err = db.Close(); err != nil {
						t.Fatal(err)
					}
					db, err = OpenDatabase(path, kind)
					if err != nil {
						t.Fatal(err)
					}
				}
				db.Close()
			})
		}
	}
}
