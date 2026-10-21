package storage

import (
	"context"
	"strconv"
	"testing"
)

func TestAgentLinkAddressModeUpgradePreservesEndpoints(t *testing.T) {
	for _, version := range []int{20, 22} {
		t.Run(strconv.Itoa(version), func(t *testing.T) {
			db := migrationDB(t)
			ctx := context.Background()
			nodeMigrationPrefix(t, db, ControlDatabase, version)
			execMigrationSQL(t, db, `INSERT INTO agent_link_host VALUES(1,'127.0.0.1:8793','tcp://node.tail.ts.net:443','tailscale');`)
			if version >= 21 {
				execMigrationSQL(t, db, `INSERT INTO agent_link_endpoints VALUES('tcp://node.tail.ts.net:443'),('tcp://192.168.1.5:8793');`)
			}
			count := migrationCount(t, db)
			// A failed conversion leaves the old mode and no receipt for version 23.
			execMigrationSQL(t, db, `CREATE TRIGGER fail_address_mode BEFORE UPDATE ON agent_link_host BEGIN SELECT RAISE(ABORT,'interrupted mode conversion'); END;`)
			if err := Migrate(ctx, db, ControlDatabase); err == nil {
				t.Fatal("failed conversion accepted")
			}
			var mode string
			if err := db.QueryRow("SELECT mode FROM agent_link_host").Scan(&mode); err != nil || mode != "tailscale" || migrationCount(t, db) != count+22-version {
				t.Fatal("failed conversion changed mode or receipt", mode, err)
			}
			execMigrationSQL(t, db, "DROP TRIGGER fail_address_mode")
			for range 2 {
				if err := Migrate(ctx, db, ControlDatabase); err != nil {
					t.Fatal(err)
				}
				var listen, endpoint string
				if err := db.QueryRow("SELECT listen,endpoint,mode FROM agent_link_host").Scan(&listen, &endpoint, &mode); err != nil || listen != "127.0.0.1:8793" || endpoint != "tcp://node.tail.ts.net:443" || mode != "address" {
					t.Fatal("host endpoint changed", listen, endpoint, mode, err)
				}
				var configured, additional int
				if err := db.QueryRow("SELECT count(*) FROM agent_link_endpoints WHERE endpoint='tcp://node.tail.ts.net:443'").Scan(&configured); err != nil || configured != 1 {
					t.Fatal("configured endpoint lost", configured, err)
				}
				if err := db.QueryRow("SELECT count(*) FROM agent_link_endpoints WHERE endpoint='tcp://192.168.1.5:8793'").Scan(&additional); err != nil || (additional == 1) != (version >= 21) {
					t.Fatal("independently enabled endpoint changed", additional, err)
				}
			}
		})
	}
}
