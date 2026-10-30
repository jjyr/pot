package storage

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestMCPUpgradePreservesHistory(t *testing.T) {
	for _, kind := range []Database{ControlDatabase, TasksDatabase} {
		t.Run(string(kind), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "upgrade.sqlite")
			db, e := OpenSQLite(path)
			if e != nil {
				t.Fatal(e)
			}
			defer db.Close()
			previous := fstest.MapFS{}
			entries, e := fs.ReadDir(migrationFiles, "migrations/"+string(kind))
			if e != nil {
				t.Fatal(e)
			}
			cutoff := "0005"
			if kind == ControlDatabase {
				cutoff = "0006"
			}
			for _, entry := range entries {
				if entry.Name() >= cutoff {
					continue
				}
				name := "migrations/" + string(kind) + "/" + entry.Name()
				raw, e := fs.ReadFile(migrationFiles, name)
				if e != nil {
					t.Fatal(e)
				}
				previous[name] = &fstest.MapFile{Data: raw}
			}
			if e = migrateDatabase(context.Background(), db, kind, previous); e != nil {
				t.Fatal(e)
			}
			// Seed immutable historical payloads before any MCP table exists. These
			// are opaque archive bytes; migrations must not reinterpret/replay them.
			preserved := map[string]string{}
			if kind == TasksDatabase {
				for _, statement := range []string{
					`INSERT INTO tasks VALUES('old-owner','historical-task','interrupted',123,'{"schema_version":14,"marker":"old-task-keep"}')`,
					`INSERT INTO task_run_history VALUES('old-owner','historical-task','historical-run','{"artifact":"old-node/report.txt","state":"unknown"}')`,
					`INSERT INTO run_workspaces VALUES('old-node','historical-task','historical-run','{"path":"/original/readonly/archive"}')`,
					`INSERT INTO retired_execution_receipts VALUES('old-node','historical-task','historical-run','uncertain-command','digest','unknown','receipt','immutable-input')`,
				} {
					if _, e = db.Exec(statement); e != nil {
						t.Fatal(e)
					}
				}
				preserved[`SELECT body FROM tasks WHERE id='historical-task'`] = `{"schema_version":14,"marker":"old-task-keep"}`
				preserved[`SELECT body FROM task_run_history WHERE task_id='historical-task'`] = `{"artifact":"old-node/report.txt","state":"unknown"}`
				preserved[`SELECT body FROM run_workspaces WHERE task_id='historical-task'`] = `{"path":"/original/readonly/archive"}`
				preserved[`SELECT phase FROM retired_execution_receipts WHERE command_id='uncertain-command'`] = `unknown`
			} else {
				intent := `{"task_default":{"profile_id":"p","model":"keep"},"other":true}`
				if _, e = db.Exec(`INSERT INTO assistant_initial_intent(id,intent) VALUES('preserved-assistant',?)`, intent); e != nil {
					t.Fatal(e)
				}
				preserved[`SELECT intent FROM assistant_initial_intent WHERE id='preserved-assistant'`] = `{"task_default":{"profile_id":"p","model":"keep","context_window":null,"max_output_tokens":null,"reasoning_effort":null},"other":true}`
				if _, e = db.Exec(`INSERT INTO agent_link_received_tasks VALUES('old-receipt','{"node_id":"old-peer","kind":"assistant","agent_id":"old-owner"}','old-request','old-owner','{"text":"immutable historical request"}','historical-task')`); e != nil {
					t.Fatal(e)
				}
				preserved[`SELECT payload FROM agent_link_received_tasks WHERE id='old-receipt'`] = `{"text":"immutable historical request"}`
			}
			// Released migration identities and timestamps must survive the pending
			// batch and a subsequent reopen without being replaced or replayed.
			receipts, e := db.Query(`SELECT id, CAST(applied_at AS TEXT) FROM pot_migrations`)
			if e != nil {
				t.Fatal(e)
			}
			for receipts.Next() {
				var id, appliedAt string
				if e = receipts.Scan(&id, &appliedAt); e != nil {
					receipts.Close()
					t.Fatal(e)
				}
				query := "SELECT CAST(applied_at AS TEXT) FROM pot_migrations WHERE id='" + strings.ReplaceAll(id, "'", "''") + "'"
				preserved[query] = appliedAt
			}
			if e = receipts.Err(); e != nil {
				receipts.Close()
				t.Fatal(e)
			}
			receipts.Close()
			// Preserve the entire prior schema, including all historical task/report tables.
			rows, e := db.Query("SELECT name,sql FROM sqlite_schema WHERE name NOT LIKE 'sqlite_%' AND name!='pot_migrations' ORDER BY name")
			if e != nil {
				t.Fatal(e)
			}
			old := map[string]string{}
			for rows.Next() {
				var name, ddl string
				if e = rows.Scan(&name, &ddl); e != nil {
					t.Fatal(e)
				}
				old[name] = ddl
			}
			rows.Close()
			if kind == TasksDatabase {
				_, e = db.Exec(`INSERT INTO mcp_receiver_journal VALUES('not-yet','not-yet','not-yet',1,'{}',NULL,NULL)`)
				if e == nil {
					t.Fatal("new schema unexpectedly present before upgrade")
				}
			}
			if e = Migrate(context.Background(), db, kind); e != nil {
				t.Fatal(e)
			}
			for name, ddl := range old {
				if kind == ControlDatabase && (strings.HasPrefix(name, "agent_link_") && (name == "agent_link_connections" || name == "agent_link_invitations" || name == "agent_link_claim_digest_formats" || name == "agent_link_host" || name == "agent_link_endpoints" || name == "agent_link_active_route") || name == "agent_adapter_endpoints" || name == "agent_adapter_host" || name == "node_identity_secret_migration") {
					continue
				}
				var got string
				if e = db.QueryRow("SELECT sql FROM sqlite_schema WHERE name=?", name).Scan(&got); e != nil {
					t.Fatal(e)
				}
				if kind == TasksDatabase && name == "agent_messages" {
					// Only the appended, nullable usage columns may differ. The
					// dedicated content-estimate upgrade test covers their contract.
					got = strings.Replace(got, ", content_estimated_tokens INTEGER CHECK(content_estimated_tokens >= 0), content_estimate_method TEXT", "", 1)
				}
				if got != ddl {
					t.Fatalf("prior schema changed %s", name)
				}
			}
			if kind == TasksDatabase {
				_, e = db.Exec(`INSERT INTO mcp_receiver_journal VALUES('c','d','claim',1,'{"schema_version":1}',NULL,NULL)`)
				if e != nil {
					t.Fatal(e)
				}
				for _, bad := range []string{`('c','d','other',1,'{}',NULL,NULL)`, `('c','new','claim',1,'{}',NULL,NULL)`, `('c','new','new',0,'{}',NULL,NULL)`, `('c','new','new',1,'invalid',NULL,NULL)`, `('c','new','new',1,'{}','t',NULL)`} {
					if _, e = db.Exec("INSERT INTO mcp_receiver_journal VALUES" + bad); e == nil {
						t.Fatal("constraint accepted", bad)
					}
				}
			} else {
				_, e = db.Exec(`INSERT INTO mcp_receiver_connections(id,url,node_id,secret_ref,state) VALUES('c','https://pot.example/mcp','node','secret-ref','disconnected')`)
				if e != nil {
					t.Fatal(e)
				}
				if _, e = db.Exec(`UPDATE mcp_receiver_connections SET state='replay'`); e == nil {
					t.Fatal("invalid state accepted")
				}
			}
			if e = Migrate(context.Background(), db, kind); e != nil {
				t.Fatal(e)
			}
			if e = db.Close(); e != nil {
				t.Fatal(e)
			}
			db, e = OpenDatabase(path, kind)
			if e != nil {
				t.Fatal(e)
			}
			defer db.Close()
			table := "mcp_receiver_connections"
			if kind == TasksDatabase {
				table = "mcp_receiver_journal"
			}
			var count int
			if e = db.QueryRow(fmt.Sprintf("SELECT count(*) FROM %s", table)).Scan(&count); e != nil || count != 1 {
				t.Fatal(count, e)
			}
			for query, want := range preserved {
				var got string
				if e = db.QueryRow(query).Scan(&got); e != nil || got != want {
					t.Fatalf("history altered: %s %q %v", query, got, e)
				}
			}
			for name := range old {
				if strings.HasPrefix(name, "mcp_") {
					t.Fatal("fixture must precede MCP", name)
				}
			}
		})
	}
}
