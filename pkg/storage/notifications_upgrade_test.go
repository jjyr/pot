package storage

import (
	"context"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestNotificationUpgradePreservesControlHistoryAndReopens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.sqlite")
	db, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	previous := fstest.MapFS{}
	err = fs.WalkDir(migrationFiles, "migrations/control", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && !strings.Contains(path, "0004_") {
			body, e := migrationFiles.ReadFile(path)
			if e != nil {
				return e
			}
			previous[path] = &fstest.MapFile{Data: body}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = migrateDatabase(context.Background(), db, ControlDatabase, previous); err != nil {
		t.Fatal(err)
	}
	before := snapshotDatabase(t, db)
	if err = Migrate(context.Background(), db, ControlDatabase); err != nil {
		t.Fatal(err)
	}
	after := snapshotDatabase(t, db)
	for table, rows := range before.Rows {
		if strings.Join(after.Rows[table], "\n") != strings.Join(rows, "\n") {
			t.Fatal("migration changed existing records", table)
		}
	}
	if _, err = db.Exec(`INSERT INTO notification_devices(id,binding,control_id,channel,enabled,permission,locale,subscription,expires_ms) VALUES('device','session','node','local',1,'granted','en','',99)`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO notification_deliveries(event,device,channel,revision,state,next_ms,updated_ms) VALUES('absent','device','local',1,'queued',0,0)`); err == nil {
		t.Fatal("delivery foreign keys not enforced")
	}
	db.Close()
	db, err = OpenDatabase(path, ControlDatabase)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err = db.QueryRow("SELECT count(*) FROM notification_devices").Scan(&count); err != nil || count != 1 {
		t.Fatal("notification installation lost on reopening", count, err)
	}
	if migrationCount(t, db) != 4 {
		t.Fatal("migration lineage not appended")
	}
}

func TestNotificationUpgradeFromInitialHistoryKeepsAssistantIdentity(t *testing.T) {
	db := migrationDB(t)
	ctx := context.Background()
	if err := migrateDatabase(ctx, db, ControlDatabase, initialSchema(t, ControlDatabase)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO assistant_definitions(id,request_id) VALUES('existing-owner','original-command')"); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, db, ControlDatabase); err != nil {
		t.Fatal(err)
	}
	var id, request string
	var deleted int
	if err := db.QueryRow("SELECT id,request_id,deleted FROM assistant_definitions").Scan(&id, &request, &deleted); err != nil || id != "existing-owner" || request != "original-command" || deleted != 0 {
		t.Fatal("skipped-version upgrade lost owner/receipt", id, request, deleted, err)
	}
	if migrationCount(t, db) != 4 {
		t.Fatal("pending migration batch was not applied")
	}
}
