package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

var databaseKinds = []Database{ControlDatabase, TasksDatabase, RunnerDatabase, ConversationDatabase, MachinesDatabase, UsageDatabase, OperationsDatabase}

type schemaObject struct{ Type, Name, Table, SQL string }
type schemaSnapshot struct {
	Objects                    []schemaObject
	Rows                       map[string][]string
	UserVersion, ApplicationID int
}

// These contracts were captured from independent empty databases after the full
// pre-release chain at 69d641c7. They include final ALTER/rebuild results, partial
// and expression indexes, constraints, defaults and migration-created records.
// They are test data, never a production migration source.
func snapshotDatabase(t *testing.T, db *sql.DB) schemaSnapshot {
	t.Helper()
	snapshot := schemaSnapshot{Rows: map[string][]string{}}
	rows, err := db.Query(`SELECT type,name,tbl_name,coalesce(sql,'') FROM sqlite_master WHERE name!='pot_migrations' AND name NOT LIKE 'sqlite_%' ORDER BY type,name`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var object schemaObject
		if err := rows.Scan(&object.Type, &object.Name, &object.Table, &object.SQL); err != nil {
			t.Fatal(err)
		}
		snapshot.Objects = append(snapshot.Objects, object)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	for _, object := range snapshot.Objects {
		if object.Type != "table" {
			continue
		}
		quote := func(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
		cols, err := db.Query("PRAGMA table_info(" + quote(object.Name) + ")")
		if err != nil {
			t.Fatal(err)
		}
		var expressions []string
		for cols.Next() {
			var cid, nn, pk int
			var name, typ string
			var dflt any
			if err := cols.Scan(&cid, &name, &typ, &nn, &dflt, &pk); err != nil {
				t.Fatal(err)
			}
			expressions = append(expressions, "quote("+quote(name)+")")
		}
		if err := cols.Err(); err != nil {
			t.Fatal(err)
		}
		cols.Close()
		query := "SELECT " + strings.Join(expressions, `||','||`) + " FROM " + quote(object.Name) + " ORDER BY " + strings.Join(expressions, ",")
		data, err := db.Query(query)
		if err != nil {
			t.Fatal(err)
		}
		snapshot.Rows[object.Name] = []string{}
		for data.Next() {
			var value string
			if err := data.Scan(&value); err != nil {
				t.Fatal(err)
			}
			snapshot.Rows[object.Name] = append(snapshot.Rows[object.Name], value)
		}
		if err := data.Err(); err != nil {
			t.Fatal(err)
		}
		data.Close()
	}
	if err := db.QueryRow("PRAGMA user_version").Scan(&snapshot.UserVersion); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("PRAGMA application_id").Scan(&snapshot.ApplicationID); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestInitialSchemasMatchPreReleaseFinalState(t *testing.T) {
	for _, kind := range databaseKinds {
		t.Run(string(kind), func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", "prerelease-schema", string(kind)+".json"))
			if err != nil {
				t.Fatal(err)
			}
			var expected schemaSnapshot
			if err := json.Unmarshal(data, &expected); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "db.sqlite")
			for range 2 {
				db, err := OpenDatabase(path, kind)
				if err != nil {
					t.Fatal(err)
				}
				got := snapshotDatabase(t, db)
				if !reflect.DeepEqual(got, expected) {
					t.Errorf("final schema/default records differ from pre-release contract: got %+v", got)
				}
				if migrationCount(t, db) != 1 {
					t.Error("initial schema must commit exactly one receipt")
				}
				rows, err := db.Query("PRAGMA foreign_key_check")
				if err != nil {
					t.Fatal(err)
				}
				if rows.Next() {
					t.Error("foreign key violation")
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				rows.Close()
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestPreReleaseDatabaseRejectedWithoutWrites(t *testing.T) {
	data, err := os.ReadFile("testdata/prerelease-history.json")
	if err != nil {
		t.Fatal(err)
	}
	var history map[Database][]string
	if err := json.Unmarshal(data, &history); err != nil {
		t.Fatal(err)
	}
	for _, kind := range databaseKinds {
		for _, mode := range []string{"original-baseline", "latest-history", "untracked-final", "empty-history", "legacy-version", "unversioned", "wal"} {
			t.Run(string(kind)+"/"+mode, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "old.sqlite")
				db, err := OpenSQLite(path)
				if err != nil {
					t.Fatal(err)
				}
				switch mode {
				case "legacy-version":
					execMigrationSQL(t, db, "PRAGMA user_version=1")
				case "unversioned":
					execMigrationSQL(t, db, "CREATE TABLE old_data(value TEXT); INSERT INTO old_data VALUES('keep all original evidence')")
				default:
					// Use the frozen final schema, independently of the new baseline SQL.
					raw, err := os.ReadFile(filepath.Join("testdata", "prerelease-schema", string(kind)+".json"))
					if err != nil {
						t.Fatal(err)
					}
					var old schemaSnapshot
					if err := json.Unmarshal(raw, &old); err != nil {
						t.Fatal(err)
					}
					for _, o := range old.Objects {
						if o.Type == "table" {
							execMigrationSQL(t, db, o.SQL)
						}
					}
					for _, typ := range []string{"index", "view", "trigger"} {
						for _, o := range old.Objects {
							if o.Type == typ {
								execMigrationSQL(t, db, o.SQL)
							}
						}
					}
					execMigrationSQL(t, db, "CREATE TABLE old_evidence(body TEXT); INSERT INTO old_evidence VALUES('original data and unverified directory binding')")
					if kind == TasksDatabase {
						execMigrationSQL(t, db, `INSERT INTO run_workspaces VALUES('node','task','run','{"state":"ready","directory_id":"legacy-dev-inode","path":"/never-open-this-fixture-path"}')`)
					}
					if mode != "untracked-final" {
						execMigrationSQL(t, db, "CREATE TABLE pot_migrations(id TEXT PRIMARY KEY,applied_at TIMESTAMP)")
						ids := history[kind]
						if mode == "original-baseline" {
							ids = ids[:1]
						}
						if mode == "empty-history" {
							ids = nil
						}
						for _, id := range ids {
							if _, err := db.Exec("INSERT INTO pot_migrations VALUES(?, '2026-01-01')", id); err != nil {
								t.Fatal(err)
							}
						}
					}
				}
				if mode == "wal" {
					execMigrationSQL(t, db, "PRAGMA journal_mode=WAL")
				}
				before := snapshotDatabase(t, db)
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
				original, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				for range 2 {
					if opened, err := OpenDatabase(path, kind); err == nil {
						opened.Close()
						t.Fatal("accepted old developer database")
					} else if !strings.Contains(err.Error(), "back up the complete root and use a new root") {
						t.Fatal("missing recovery guidance", err)
					}
					rejected, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(rejected, original) {
						t.Fatal("rejection modified database bytes")
					}
				}
				db, err = OpenDatabaseReadOnly(path, kind)
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				if !reflect.DeepEqual(snapshotDatabase(t, db), before) {
					t.Fatal("rejection modified schema/data")
				}
				// Caller-owned handles must have the same no-write boundary.
				if err := Migrate(context.Background(), db, kind); err == nil {
					t.Fatal("Migrate adopted an old database")
				}
			})
		}
	}
}

func BenchmarkFreshDatabase(b *testing.B) {
	for _, kind := range databaseKinds {
		b.Run(string(kind), func(b *testing.B) {
			root := b.TempDir()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				db, err := OpenDatabase(filepath.Join(root, fmt.Sprintf("%d.sqlite", i)), kind)
				if err != nil {
					b.Fatal(err)
				}
				if err := db.Close(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestInitialSchemaFailureRollsBackAndRetries(t *testing.T) {
	db := migrationDB(t)
	// A connection authorizer is unnecessary: a deferred foreign key lets the
	// entire initial script run and then fail at commit.
	files := migrationFixture(`CREATE TABLE parent(id INTEGER PRIMARY KEY); CREATE TABLE child(id INTEGER REFERENCES parent(id) DEFERRABLE INITIALLY DEFERRED); INSERT INTO child VALUES(1);`)
	if err := migrateDatabase(t.Context(), db, TasksDatabase, files); err == nil {
		t.Fatal("invalid initial transaction committed")
	}
	for _, table := range []string{"parent", "child"} {
		if exists, err := tableExists(t.Context(), db, table); err != nil || exists {
			t.Fatal("failed baseline leaked schema", table, err)
		}
	}
	if migrationCount(t, db) != 0 {
		t.Fatal("failed baseline left receipt")
	}
	files = migrationFixture(`CREATE TABLE parent(id INTEGER PRIMARY KEY); CREATE TABLE child(id INTEGER REFERENCES parent(id) DEFERRABLE INITIALLY DEFERRED); INSERT INTO parent VALUES(1); INSERT INTO child VALUES(1);`)
	for range 2 {
		if err := migrateDatabase(t.Context(), db, TasksDatabase, files); err != nil {
			t.Fatal("empty history could not retry", err)
		}
	}
	if migrationCount(t, db) != 1 {
		t.Fatal("retry did not commit once")
	}
}

func TestInitialSchemaConstraintsAndMailboxViews(t *testing.T) {
	db, err := OpenDatabase(filepath.Join(t.TempDir(), "tasks.sqlite"), TasksDatabase)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, bad := range []string{
		`INSERT INTO assistant_mail_identity VALUES(2,'wrong-singleton')`,
		`INSERT INTO codex_link_assistants(id,name,workspace,mode) VALUES('bad','Bad','/fixture','unsafe')`,
		`INSERT INTO codex_task_inputs VALUES('missing-job','request','text','run')`,
	} {
		if _, err := db.Exec(bad); err == nil {
			t.Fatalf("constraint missing: %s", bad)
		}
	}
	execMigrationSQL(t, db, `INSERT INTO codex_link_assistants(id,name,workspace,mode,is_default) VALUES('first','First','/fixture','read-only',1)`)
	if _, err := db.Exec(`INSERT INTO codex_link_assistants(id,name,workspace,mode,is_default) VALUES('second','Second','/fixture','read-only',1)`); err == nil {
		t.Fatal("partial unique default index missing")
	}
	execMigrationSQL(t, db, `INSERT INTO agent_messages(id,envelope,direction,state,conversation,recipient) VALUES('input','{"kind":"task_input","to":{"kind":"task"},"owner":"owner","request_id":"request","text":"retained"}','in','queued','run','task')`)
	var accepted int
	if err := db.QueryRow("SELECT accepted_at_ms FROM agent_messages WHERE id='input'").Scan(&accepted); err != nil || accepted != 0 {
		t.Fatal("timestamp default", accepted, err)
	}
	execMigrationSQL(t, db, `UPDATE task_inputs SET state='consumed' WHERE request_id='request'`)
	var state string
	if err := db.QueryRow("SELECT state FROM agent_messages WHERE id='input'").Scan(&state); err != nil || state != "consumed" {
		t.Fatal("view update trigger", state, err)
	}
}
