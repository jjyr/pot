package storage

import (
	"context"
	"io/fs"
	"testing"
	"testing/fstest"
)

func TestExtensionsUpgradePreservesExistingControlAndRepeatedStartup(t *testing.T) {
	db := migrationDB(t)
	ctx := context.Background()
	prior := fstest.MapFS{}
	paths, err := fs.Glob(migrationFiles, "migrations/control/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		if path >= "migrations/control/0013_" {
			continue
		}
		data, err := migrationFiles.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		prior[path] = &fstest.MapFile{Data: data}
	}
	if err = migrateDatabase(ctx, db, ControlDatabase, prior); err != nil {
		t.Fatal(err)
	}
	execMigrationSQL(t, db, "INSERT INTO assistant_definitions(id,name,revision,request_id,created_name) VALUES('main','Original',7,'original','Original')")
	for range 2 {
		if err = migrateDatabase(ctx, db, ControlDatabase, migrationsBefore(t, ControlDatabase, "0032_")); err != nil {
			t.Fatal(err)
		}
	}
	var name string
	var revision int
	if err = db.QueryRow("SELECT name,revision FROM assistant_definitions WHERE id='main'").Scan(&name, &revision); err != nil || name != "Original" || revision != 7 {
		t.Fatal(name, revision, err)
	}
	execMigrationSQL(t, db, "INSERT INTO retired_extension_settings VALUES('plugins','github',0,1,'');INSERT INTO retired_mcp_servers VALUES('test',1,'{}')")
	if err = migrateDatabase(ctx, db, ControlDatabase, migrationsBefore(t, ControlDatabase, "0032_")); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = db.QueryRow("SELECT COUNT(*) FROM retired_extension_settings WHERE enabled=0").Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
}
