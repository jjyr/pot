package storage

import (
	"context"
	"io/fs"
	"strconv"
	"testing"
	"testing/fstest"
)

func TestPotAddressesUpgradePreservesPriorEndpoint(t *testing.T) {
	for _, version := range []int{18, 20} {
		t.Run(strconv.Itoa(version), func(t *testing.T) {
			db := migrationDB(t)
			ctx := context.Background()
			prior := fstest.MapFS{}
			entries, _ := fs.ReadDir(migrationFiles, "migrations/control")
			for index, entry := range entries {
				if index >= version {
					break
				}
				path := "migrations/control/" + entry.Name()
				raw, _ := fs.ReadFile(migrationFiles, path)
				prior[path] = &fstest.MapFile{Data: raw}
			}
			if err := migrateDatabase(ctx, db, ControlDatabase, prior); err != nil {
				t.Fatal(err)
			}
			execMigrationSQL(t, db, "INSERT INTO agent_link_host VALUES(1,'127.0.0.1:9876','tcp://100.88.0.4:9876','address')")
			for range 2 {
				if err := Migrate(ctx, db, ControlDatabase); err != nil {
					t.Fatal(err)
				}
			}
			var endpoint, listen, mode string
			if err := db.QueryRow("SELECT endpoint FROM agent_link_endpoints").Scan(&endpoint); err != nil || endpoint != "tcp://100.88.0.4:9876" {
				t.Fatal(endpoint, err)
			}
			if err := db.QueryRow("SELECT listen,mode FROM agent_link_host").Scan(&listen, &mode); err != nil || listen != "127.0.0.1:9876" || mode != "address" {
				t.Fatal(listen, mode, err)
			}
		})
	}
}
