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

func TestMCPNodeExecutionUpgradeFromPreviousVersion(t *testing.T) {
	db, e := OpenSQLite(filepath.Join(t.TempDir(), "node-execution-upgrade.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	previous := fstest.MapFS{}
	entries, e := fs.ReadDir(migrationFiles, "migrations/tasks")
	if e != nil {
		t.Fatal(e)
	}
	for _, entry := range entries {
		if entry.Name() >= "0009" {
			continue
		}
		name := "migrations/tasks/" + entry.Name()
		raw, e := fs.ReadFile(migrationFiles, name)
		if e != nil {
			t.Fatal(e)
		}
		previous[name] = &fstest.MapFile{Data: raw}
	}
	if e = migrateDatabase(context.Background(), db, TasksDatabase, previous); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`INSERT INTO mcp_receiver_journal VALUES('c','d','claim',17,'{"schema_version":2,"outbox":{"result":"unknown-keep"}}','real-task','real-run')`); e != nil {
		t.Fatal(e)
	}
	if e = Migrate(context.Background(), db, TasksDatabase); e != nil {
		t.Fatal(e)
	}
	var body string
	var revision int
	if e = db.QueryRow("SELECT body,revision FROM mcp_receiver_journal").Scan(&body, &revision); e != nil || body != `{"schema_version":2,"outbox":{"result":"unknown-keep"}}` || revision != 17 {
		t.Fatal("node attestation migration changed accepted work", body, revision, e)
	}
	if _, e = db.Exec(`INSERT INTO mcp_node_executions VALUES('c','task','execution','{"actor":"same","run":"real"}','',1)`); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`UPDATE mcp_node_executions SET active=2`); e == nil {
		t.Fatal("invalid execution liveness accepted")
	}
	if e = Migrate(context.Background(), db, TasksDatabase); e != nil {
		t.Fatal(e)
	}
	var binding string
	if e = db.QueryRow("SELECT binding FROM mcp_node_executions").Scan(&binding); e != nil || binding != `{"actor":"same","run":"real"}` {
		t.Fatal("attestation changed on reentry", binding, e)
	}
}
