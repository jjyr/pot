package storage

import (
	"context"
	"io/fs"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestMCPExecutionGatewayUpgrade(t *testing.T) {
	for _, kind := range []Database{ControlDatabase, TasksDatabase} {
		t.Run(string(kind), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "upgrade.sqlite")
			db, e := OpenSQLite(path)
			if e != nil {
				t.Fatal(e)
			}
			defer db.Close()
			preceding := fstest.MapFS{}
			entries, e := fs.ReadDir(migrationFiles, "migrations/"+string(kind))
			if e != nil {
				t.Fatal(e)
			}
			limit := "0007"
			if kind == TasksDatabase {
				limit = "0008"
			}
			for _, entry := range entries {
				if entry.Name() >= limit {
					continue
				}
				name := "migrations/" + string(kind) + "/" + entry.Name()
				raw, e := fs.ReadFile(migrationFiles, name)
				if e != nil {
					t.Fatal(e)
				}
				preceding[name] = &fstest.MapFile{Data: raw}
			}
			if e = migrateDatabase(context.Background(), db, kind, preceding); e != nil {
				t.Fatal(e)
			}
			if kind == TasksDatabase {
				_, e = db.Exec(`INSERT INTO mcp_receiver_journal VALUES('connection','delivery','claim',9,'{"schema_version":1,"claim":{"claim_id":"claim"},"outbox":{"unknown_effect":"retain"}}','real-task','real-run')`)
			} else {
				_, e = db.Exec(`INSERT INTO mcp_receiver_connections(id,url,node_id,secret_ref,state) VALUES('connection','https://pot.example/mcp','local-node','encrypted-ref','disconnected')`)
			}
			if e != nil {
				t.Fatal(e)
			}
			if e = Migrate(context.Background(), db, kind); e != nil {
				t.Fatal(e)
			}
			if kind == TasksDatabase {
				var version, revision int
				var task, run, outbox string
				e = db.QueryRow(`SELECT json_extract(body,'$.schema_version'),revision,execution_task,execution_run,json_extract(body,'$.outbox.unknown_effect') FROM mcp_receiver_journal`).Scan(&version, &revision, &task, &run, &outbox)
				if e != nil || version != 2 || revision != 9 || task != "real-task" || run != "real-run" || outbox != "retain" {
					t.Fatal(version, revision, task, run, outbox, e)
				}
			} else {
				var state, remote, secret string
				e = db.QueryRow("SELECT state,remote_node_id,secret_ref FROM mcp_receiver_connections").Scan(&state, &remote, &secret)
				if e != nil || state != "disconnected" || remote != "" || secret != "encrypted-ref" {
					t.Fatal(state, remote, secret, e)
				}
			}
			if e = Migrate(context.Background(), db, kind); e != nil {
				t.Fatal(e)
			}
		})
	}
}
