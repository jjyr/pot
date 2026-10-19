package storage

import (
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
)

// OpenDatabaseReadOnly reads an existing database without initializing its
// schema or applying migrations. It is intended for inspecting the credentials
// of an already-running owner before deciding whether its version can be reused.
// The caller must understand the fields it reads from that owner's schema.
// It never creates the database, changes journal mode or acquires a migration lock.
func OpenDatabaseReadOnly(path string, kind Database) (*sql.DB, error) {
	if !knownDatabase(kind) {
		return nil, fmt.Errorf("unknown database kind %q", kind)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: abs}
	q := u.Query()
	q.Set("mode", "ro")
	q.Add("_pragma", "query_only(ON)")
	q.Add("_pragma", "busy_timeout(5000)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err = db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}
