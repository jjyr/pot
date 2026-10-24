package storage

import (
	"context"
	"database/sql"

	"fmt"

	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
)

func migrationDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := OpenSQLite(filepath.Join(t.TempDir(), "test.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func migrationFixture(statements ...string) fstest.MapFS {
	files := fstest.MapFS{}
	for index, statement := range statements {
		files[fmt.Sprintf("migrations/tasks/%04d_step.sql", index+1)] = &fstest.MapFile{Data: []byte("-- +migrate Up\n-- +migrate StatementBegin\n" + statement + "\n-- +migrate StatementEnd\n")}
	}
	return files
}

func execMigrationSQL(t *testing.T, db *sql.DB, statement string) {
	t.Helper()
	if _, err := db.Exec(statement); err != nil {
		t.Fatal(err)
	}
}

func migrationCount(t *testing.T, db *sql.DB) int {
	t.Helper()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM pot_migrations").Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestDatabaseSchemasCreateAndReopen(t *testing.T) {
	for _, kind := range []Database{ControlDatabase, TasksDatabase, RunnerDatabase, ConversationDatabase, MachinesDatabase, UsageDatabase, OperationsDatabase} {
		t.Run(string(kind), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "db.sqlite")
			db, err := OpenDatabase(path, kind)
			if err != nil {
				t.Fatal(err)
			}
			var integrity string
			if err := db.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
				t.Fatal(integrity, err)
			}
			definitions, err := loadMigrations(migrationFiles, kind)
			if err != nil {
				t.Fatal(err)
			}
			if got := migrationCount(t, db); got != len(definitions) {
				t.Fatal(got, len(definitions))
			}
			db.Close()
			db, err = OpenDatabase(path, kind)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if got := migrationCount(t, db); got != len(definitions) {
				t.Fatal(got)
			}
		})
	}
}

func TestMigrationSkipsVersionsAndTransformsJSONOnce(t *testing.T) {
	db := migrationDB(t)
	ctx := context.Background()
	first := `CREATE TABLE records(id TEXT PRIMARY KEY,body TEXT NOT NULL); INSERT INTO records VALUES('task','{"schema_version":1,"goal":"keep me"}');`
	files := migrationFixture(first)
	if err := migrateDatabase(ctx, db, TasksDatabase, files); err != nil {
		t.Fatal(err)
	}
	files = migrationFixture(first,
		`ALTER TABLE records ADD COLUMN revision INTEGER NOT NULL DEFAULT 0; UPDATE records SET body=json_set(body,'$.schema_version',2,'$.instructions',json_extract(body,'$.goal'));`,
		`UPDATE records SET body=json_remove(body,'$.goal'),revision=revision+1; CREATE INDEX records_revision ON records(revision);`)
	for range 2 {
		if err := migrateDatabase(ctx, db, TasksDatabase, files); err != nil {
			t.Fatal(err)
		}
	}
	var body string
	var revision int
	if err := db.QueryRow("SELECT body,revision FROM records WHERE id='task'").Scan(&body, &revision); err != nil {
		t.Fatal(err)
	}
	if body != `{"schema_version":2,"instructions":"keep me"}` || revision != 1 || migrationCount(t, db) != 3 {
		t.Fatal(body, revision)
	}
}

func TestMigrationFailureRollsBackAndRetries(t *testing.T) {
	db := migrationDB(t)
	ctx := context.Background()
	first := `CREATE TABLE records(value INTEGER NOT NULL); INSERT INTO records VALUES(10);`
	if err := migrateDatabase(ctx, db, TasksDatabase, migrationFixture(first)); err != nil {
		t.Fatal(err)
	}
	files := migrationFixture(first, `ALTER TABLE records ADD COLUMN added TEXT; UPDATE records SET value=value+1; INSERT INTO ready VALUES(1);`)
	if err := migrateDatabase(ctx, db, TasksDatabase, files); err == nil {
		t.Fatal("missing prerequisite did not fail")
	}
	var value, added int
	if err := db.QueryRow("SELECT value FROM records").Scan(&value); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('records') WHERE name='added'").Scan(&added); err != nil {
		t.Fatal(err)
	}
	if value != 10 || added != 0 || migrationCount(t, db) != 1 {
		t.Fatal(value, added)
	}
	// Resolve the environmental prerequisite; retry the exact same migration.
	execMigrationSQL(t, db, "CREATE TABLE ready(value INTEGER)")
	if err := migrateDatabase(ctx, db, TasksDatabase, files); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT value FROM records").Scan(&value); err != nil || value != 11 {
		t.Fatal(value, err)
	}
	if migrationCount(t, db) != 2 {
		t.Fatal("failed migration was not retried")
	}
}

func TestMigrationRejectsInvalidHistoryBeforeWriting(t *testing.T) {
	for _, mode := range []string{"changed", "newer", "gap", "wrong-database"} {
		t.Run(mode, func(t *testing.T) {
			db := migrationDB(t)
			ctx := context.Background()
			files := migrationFixture("CREATE TABLE records(value INTEGER); INSERT INTO records VALUES(1);", "UPDATE records SET value=2;")
			if err := migrateDatabase(ctx, db, TasksDatabase, files); err != nil {
				t.Fatal(err)
			}
			kind := TasksDatabase
			switch mode {
			case "changed":
				files["migrations/tasks/0001_step.sql"].Data = append(files["migrations/tasks/0001_step.sql"].Data, []byte("-- changed\n")...)
			case "newer":
				delete(files, "migrations/tasks/0002_step.sql")
			case "gap":
				execMigrationSQL(t, db, "DELETE FROM pot_migrations WHERE id LIKE '0001_%'")
			case "wrong-database":
				kind = ControlDatabase
				files["migrations/control/0001_step.sql"] = files["migrations/tasks/0001_step.sql"]
			}
			before := migrationCount(t, db)
			if err := migrateDatabase(ctx, db, kind, files); err == nil || !strings.Contains(err.Error(), "history mismatch") {
				t.Fatal(err)
			}
			var value int
			if err := db.QueryRow("SELECT value FROM records").Scan(&value); err != nil || value != 2 {
				t.Fatal(value, err)
			}
			if migrationCount(t, db) != before {
				t.Fatal("rejected history was changed")
			}
		})
	}
}

func TestMigrationRejectsInvalidDefinitions(t *testing.T) {
	for _, mode := range []string{"gap", "duplicate", "down", "nontransactional", "empty", "unknown-kind"} {
		t.Run(mode, func(t *testing.T) {
			db := migrationDB(t)
			files := migrationFixture("CREATE TABLE records(value INTEGER);")
			kind := TasksDatabase
			switch mode {
			case "gap":
				files["migrations/tasks/0003_step.sql"] = files["migrations/tasks/0001_step.sql"]
			case "duplicate":
				files["migrations/tasks/0001_other.sql"] = files["migrations/tasks/0001_step.sql"]
			case "down":
				files["migrations/tasks/0001_step.sql"].Data = append(files["migrations/tasks/0001_step.sql"].Data, []byte("-- +migrate Down\nDROP TABLE records;\n")...)
			case "nontransactional":
				files["migrations/tasks/0001_step.sql"].Data = []byte("-- +migrate Up notransaction\nCREATE TABLE records(value INTEGER);\n")
			case "empty":
				files["migrations/tasks/0001_step.sql"].Data = []byte("-- +migrate Up\n")
			case "unknown-kind":
				kind = "../tasks"
			}
			if err := migrateDatabase(context.Background(), db, kind, files); err == nil {
				t.Fatal("accepted invalid definition")
			}
			if exists, err := tableExists(context.Background(), db, "pot_migrations"); err != nil || exists {
				t.Fatal(exists, err)
			}
		})
	}
}

func TestMigrationConcurrentHandles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared.sqlite")
	// Open handles first; OpenSQLite's journal PRAGMA itself is not a migration.
	var handles []*sql.DB
	for range 4 {
		db, err := OpenSQLite(path)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		handles = append(handles, db)
	}
	var wg sync.WaitGroup
	for _, db := range handles {
		wg.Go(func() {
			if err := Migrate(context.Background(), db, TasksDatabase); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	definitions, err := loadMigrations(migrationFiles, TasksDatabase)
	if err != nil {
		t.Fatal(err)
	}
	if got := migrationCount(t, handles[0]); got != len(definitions) {
		t.Fatal(got)
	}
}

func TestMigrationCancellationDoesNotWrite(t *testing.T) {
	db := migrationDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Migrate(ctx, db, TasksDatabase); err != context.Canceled {
		t.Fatal(err)
	}
	if exists, err := tableExists(context.Background(), db, "pot_migrations"); err != nil || exists {
		t.Fatal(exists, err)
	}
}

func TestMessageFilesUpgradePreservesResources(t *testing.T) {
	db := migrationDB(t)
	ctx := context.Background()
	first, err := migrationFiles.ReadFile("migrations/control/0001_initial_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	files := fstest.MapFS{"migrations/control/0001_initial_schema.sql": {Data: first}}
	if err = migrateDatabase(ctx, db, ControlDatabase, files); err != nil {
		t.Fatal(err)
	}
	execMigrationSQL(t, db, `INSERT INTO display_resources VALUES('old','main','request','hash','{"id":"old"}',0,1)`)
	if err = migrateDatabase(ctx, db, ControlDatabase, migrationFiles); err != nil {
		t.Fatal(err)
	}
	var metadata string
	if err = db.QueryRow(`SELECT metadata FROM display_resources WHERE id='old'`).Scan(&metadata); err != nil || metadata != `{"id":"old"}` {
		t.Fatal(metadata, err)
	}
	execMigrationSQL(t, db, `INSERT INTO message_files(owner,conversation,message,revision,ref,start,end,href,label,block,source,state) VALUES('main','main','reply','rev','ref',0,12,'file.txt','File',1,'{}','pending')`)
	if err = migrateDatabase(ctx, db, ControlDatabase, migrationFiles); err != nil {
		t.Fatal(err)
	}
}
