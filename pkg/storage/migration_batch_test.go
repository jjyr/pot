package storage

import (
	"bufio"
	"context"
	"database/sql"
	"database/sql/driver"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	migrate "github.com/rubenv/sql-migrate"
	"modernc.org/sqlite"
)

func batchFixture() []string {
	return []string{
		`CREATE TABLE receipt(id TEXT PRIMARY KEY,value INTEGER); INSERT INTO receipt VALUES('effect',10);`,
		`ALTER TABLE receipt ADD COLUMN converted INTEGER DEFAULT 0; UPDATE receipt SET value=value+1,converted=1;`,
		`SELECT pot_test_batch_barrier(); UPDATE receipt SET value=value+1;`,
	}
}

func init() {
	// Test-only barrier exposes a real uncommitted DDL+DML batch to the parent.
	sqlite.MustRegisterDeterministicScalarFunction("pot_test_batch_barrier", 0, func(_ *sqlite.FunctionContext, _ []driver.Value) (driver.Value, error) {
		if os.Getenv("POT_TEST_BATCH_CRASH") == "1" {
			_, _ = os.Stdout.WriteString("in-transaction\n")
			_, _ = io.Copy(io.Discard, os.Stdin)
		}
		return int64(1), nil
	})
}

func TestMigrationBatchCrashProcess(t *testing.T) {
	path := os.Getenv("POT_TEST_BATCH_PATH")
	if path == "" {
		return
	}
	db, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := migrateDatabase(t.Context(), db, TasksDatabase, migrationFixture(batchFixture()...)); err != nil {
		t.Fatal(err)
	}
}

func TestWholeMigrationBatchCrashAndReentry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.sqlite")
	db, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	steps := batchFixture()
	if err := migrateDatabase(t.Context(), db, TasksDatabase, migrationFixture(steps[0])); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestMigrationBatchCrashProcess$")
	cmd.Env = append(os.Environ(), "POT_TEST_BATCH_PATH="+path, "POT_TEST_BATCH_CRASH=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil || line != "in-transaction\n" {
		cmd.Process.Kill()
		cmd.Wait()
		t.Fatal(line, err)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("crash fixture exited successfully")
	}
	readonly, readErr := OpenDatabaseReadOnly(path, TasksDatabase)
	if readErr != nil {
		t.Fatal("crashed database cannot be inspected before startup:", readErr)
	}
	var inspected int
	readErr = readonly.QueryRow("SELECT value FROM receipt").Scan(&inspected)
	readonly.Close()
	if readErr != nil {
		t.Fatal("crashed database read-only history preflight:", readErr)
	}
	var value, added int
	if err := db.QueryRow("SELECT value FROM receipt").Scan(&value); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT count(*) FROM pragma_table_info('receipt') WHERE name='converted'").Scan(&added); err != nil {
		t.Fatal(err)
	}
	if value != 10 || added != 0 || migrationCount(t, db) != 1 {
		t.Fatal("crash leaked pending batch", value, added)
	}
	for range 2 {
		if err := migrateDatabase(t.Context(), db, TasksDatabase, migrationFixture(steps...)); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.QueryRow("SELECT value FROM receipt").Scan(&value); err != nil || value != 12 || migrationCount(t, db) != 3 {
		t.Fatal("batch did not convert exactly once", value, err)
	}
}

func TestWholeMigrationBatchFailureRollsBackEarlierFile(t *testing.T) {
	db := migrationDB(t)
	steps := batchFixture()
	if err := migrateDatabase(t.Context(), db, TasksDatabase, migrationFixture(steps[0])); err != nil {
		t.Fatal(err)
	}
	steps[2] = `UPDATE receipt SET value=value+100; INSERT INTO missing_table VALUES(1);`
	if err := migrateDatabase(t.Context(), db, TasksDatabase, migrationFixture(steps...)); err == nil {
		t.Fatal("invalid batch committed")
	}
	var value, added int
	if err := db.QueryRow("SELECT value FROM receipt").Scan(&value); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT count(*) FROM pragma_table_info('receipt') WHERE name='converted'").Scan(&added); err != nil {
		t.Fatal(err)
	}
	if value != 10 || added != 0 || migrationCount(t, db) != 1 {
		t.Fatal("earlier pending file survived failure", value, added)
	}
}

func TestMigrationBatchConcurrentConvertsOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.sqlite")
	db, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	steps := batchFixture()
	if err := migrateDatabase(t.Context(), db, TasksDatabase, migrationFixture(steps[0])); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			handle, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)")
			if err != nil {
				t.Error(err)
				return
			}
			defer handle.Close()
			if err := migrateDatabase(context.Background(), handle, TasksDatabase, migrationFixture(steps...)); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	var value int
	if err := db.QueryRow("SELECT value FROM receipt").Scan(&value); err != nil || value != 12 || migrationCount(t, db) != 3 {
		t.Fatal(value, err)
	}
}

func TestMigrationForbidsTransactionEscapeAndExternalEffects(t *testing.T) {
	for _, sql := range []string{"COMMIT;", "PRAGMA foreign_keys=OFF;", "ATTACH DATABASE 'other' AS other;", "VACUUM;", "SELECT load_extension('other');", "END;", "SAVEPOINT escape;"} {
		if err := validateMigrationSQL(sql); err == nil {
			t.Errorf("accepted %s", sql)
		}
	}
}

func TestMigrationBatchConcurrentProcesses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.sqlite")
	db, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := migrateDatabase(t.Context(), db, TasksDatabase, migrationFixture(batchFixture()[0])); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	var commands []*exec.Cmd
	for range 4 {
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestMigrationBatchCrashProcess$")
		command.Env = append(os.Environ(), "POT_TEST_BATCH_PATH="+path)
		command.Stdout, command.Stderr = io.Discard, os.Stderr
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		commands = append(commands, command)
	}
	for _, command := range commands {
		if err := command.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	var value int
	if err := db.QueryRow("SELECT value FROM receipt").Scan(&value); err != nil || value != 12 || migrationCount(t, db) != 3 {
		t.Fatal("concurrent processes repeated conversion", value, err)
	}
}

func TestFixedLedgerAcceptsSQLMigrateLayoutAndRejectsDrift(t *testing.T) {
	db := migrationDB(t)
	files := migrationFixture(batchFixture()[0])
	definitions, err := loadMigrations(files, TasksDatabase)
	if err != nil {
		t.Fatal(err)
	}
	set := migrate.MigrationSet{TableName: "pot_migrations"}
	if _, err := set.ExecContext(t.Context(), db, "sqlite3", &migrate.MemoryMigrationSource{Migrations: definitions}, migrate.Up); err != nil {
		t.Fatal(err)
	}
	if err := migrateDatabase(t.Context(), db, TasksDatabase, files); err != nil {
		t.Fatal("original sql-migrate ledger no longer accepted", err)
	}
	for _, layout := range []string{
		"CREATE TABLE pot_migrations(id TEXT PRIMARY KEY,applied_at DATETIME)",
		"CREATE TABLE pot_migrations(id VARCHAR(255) NOT NULL PRIMARY KEY,applied_at DATETIME,extra INTEGER)",
	} {
		broken := migrationDB(t)
		execMigrationSQL(t, broken, layout)
		if err := migrateDatabase(t.Context(), broken, TasksDatabase, files); err == nil {
			t.Fatal("accepted ledger drift", layout)
		}
		if exists, err := tableExists(t.Context(), broken, "receipt"); err != nil || exists {
			t.Fatal("drift rejection wrote product schema", err)
		}
	}
}
