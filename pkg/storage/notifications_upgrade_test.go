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

// Cover released history, skipped upgrades without
// duplicating whole-database fixtures. The unmerged 0004 has no released lineage.
func TestNotificationConsumerUpgradePreservesHistoryAndSecrets(t *testing.T) {
	for _, version := range []int{1, 3} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "control.sqlite")
			db, err := OpenSQLite(path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			prefix := fstest.MapFS{}
			err = fs.WalkDir(migrationFiles, "migrations/control", func(path string, e fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if e.IsDir() {
					return nil
				}
				var n int
				if _, err = fmt.Sscanf(e.Name(), "%04d_", &n); err != nil {
					return err
				}
				if n <= version {
					body, err := migrationFiles.ReadFile(path)
					if err != nil {
						return err
					}
					prefix[path] = &fstest.MapFile{Data: body}
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if err = migrateDatabase(context.Background(), db, ControlDatabase, prefix); err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec("INSERT INTO assistant_definitions(id,request_id) VALUES('owner','original-command')"); err != nil {
				t.Fatal(err)
			}
			if err = Migrate(context.Background(), db, ControlDatabase); err != nil {
				t.Fatal(err)
			}
			var request string
			if err = db.QueryRow("SELECT request_id FROM assistant_definitions WHERE id='owner'").Scan(&request); err != nil || request != "original-command" {
				t.Fatal("owner history changed", request, err)
			}
			if _, err = db.Exec(`INSERT INTO notification_devices(id,binding,control_id,channel,enabled,permission,locale,expires_ms) VALUES('fresh','session','node','local',1,'granted','en',99)`); err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec(`INSERT INTO notification_deliveries(event,device,channel,revision,state,next_ms,updated_ms) VALUES('absent','fresh','local',1,'queued',0,0)`); err == nil {
				t.Fatal("delivery foreign keys not enforced")
			}
			before := migrationCount(t, db)
			db.Close()
			db, err = OpenDatabase(path, ControlDatabase)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if migrationCount(t, db) != before || before != 4 {
				t.Fatal("upgrade/reopen lineage changed")
			}
			var rows string
			if err = db.QueryRow("SELECT group_concat(id) FROM notification_devices").Scan(&rows); err != nil || !strings.Contains(rows, "fresh") {
				t.Fatal("installations lost on reopening", rows, err)
			}
		})
	}
}
