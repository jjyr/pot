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
			for _, entry := range entries {
				if entry.Name() >= "0005" {
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
				var got string
				if e = db.QueryRow("SELECT sql FROM sqlite_schema WHERE name=?", name).Scan(&got); e != nil || got != ddl {
					t.Fatalf("prior schema changed %s: %v", name, e)
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
			for name := range old {
				if strings.HasPrefix(name, "mcp_") {
					t.Fatal("fixture must precede MCP", name)
				}
			}
		})
	}
}
