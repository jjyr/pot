package storage

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestReadOnlyDatabaseNeverCreatesOrMigrates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "control.sqlite")
	if db, err := OpenDatabaseReadOnly(path, ControlDatabase); err == nil {
		db.Close()
		t.Fatal("read-only open created a database")
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatal("read-only open created a directory", err)
	}
	path = filepath.Join(t.TempDir(), "existing.sqlite")
	db, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("CREATE TABLE existing(value TEXT); INSERT INTO existing VALUES('original'); CREATE TABLE pot_migrations(id TEXT PRIMARY KEY); INSERT INTO pot_migrations VALUES('unknown-owner-schema')"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	db, err = OpenDatabaseReadOnly(path, ControlDatabase)
	if err != nil {
		t.Fatal(err)
	}
	var value string
	if err = db.QueryRow("SELECT value FROM existing").Scan(&value); err != nil || value != "original" {
		t.Fatal(value, err)
	}
	if _, err = db.Exec("PRAGMA query_only=OFF"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("UPDATE existing SET value='changed'"); err == nil {
		t.Fatal("SQLite read-only mode permitted a write")
	}
	db.Close()
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("read-only open changed schema, migration history or data", err)
	}
}
