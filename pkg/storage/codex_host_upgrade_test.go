package storage

import (
	"context"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

func TestCodexHostUpgradePreservesExistingControlData(t *testing.T) {
	ctx := context.Background()
	db := migrationDB(t)
	prior := fstest.MapFS{}
	err := fs.WalkDir(migrationFiles, "migrations/control", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && !strings.HasSuffix(path, "0005_codex_host.sql") {
			raw, e := fs.ReadFile(migrationFiles, path)
			if e != nil {
				return e
			}
			prior[path] = &fstest.MapFile{Data: raw}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = migrateDatabase(ctx, db, ControlDatabase, prior); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO providers VALUES('fixture','{\"keep\":true}')"); err != nil {
		t.Fatal(err)
	}
	if err = Migrate(ctx, db, ControlDatabase); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO codex_agent_host VALUES(1,'stable-codex','encrypted-ref','https://codex.example')"); err != nil {
		t.Fatal(err)
	}
	if err = Migrate(ctx, db, ControlDatabase); err != nil {
		t.Fatal(err)
	}
	var metadata, id string
	if err = db.QueryRow("SELECT metadata FROM providers WHERE id='fixture'").Scan(&metadata); err != nil || metadata != "{\"keep\":true}" {
		t.Fatal(metadata, err)
	}
	if err = db.QueryRow("SELECT control_id FROM codex_agent_host").Scan(&id); err != nil || id != "stable-codex" {
		t.Fatal(id, err)
	}
}
