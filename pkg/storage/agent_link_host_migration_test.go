package storage

import (
	"context"
	"testing"
	"testing/fstest"
)

func TestAgentLinkHostUpgradeAndRepeatedStartup(t *testing.T) {
	ctx := context.Background()
	db := migrationDB(t)
	old := fstest.MapFS{}
	for _, name := range []string{"0001_baseline.sql", "0002_agent_links.sql", "0003_agent_link_tcp.sql"} {
		path := "migrations/control/" + name
		data, err := migrationFiles.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		old[path] = &fstest.MapFile{Data: data}
	}
	if err := migrateDatabase(ctx, db, ControlDatabase, old); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := Migrate(ctx, db, ControlDatabase); err != nil {
			t.Fatal(err)
		}
	}
	execMigrationSQL(t, db, `INSERT INTO agent_link_host(singleton,listen,endpoint,mode) VALUES(1,'127.0.0.1:1234','tcp://127.0.0.1:1234','address')`)
	if err := Migrate(ctx, db, ControlDatabase); err != nil {
		t.Fatal(err)
	}
	var listen, endpoint, mode string
	if err := db.QueryRow(`SELECT listen,endpoint,mode FROM agent_link_host WHERE singleton=1`).Scan(&listen, &endpoint, &mode); err != nil {
		t.Fatal(err)
	}
	if listen != "127.0.0.1:1234" || endpoint != "tcp://127.0.0.1:1234" || mode != "address" {
		t.Fatal(listen, endpoint, mode)
	}
}

func TestAgentNetworkUpgradeRequiresExplicitTCPPublication(t *testing.T) {
	ctx := context.Background()
	db := migrationDB(t)
	old := fstest.MapFS{}
	for _, name := range []string{"0001_baseline.sql", "0002_agent_links.sql", "0003_agent_link_tcp.sql", "0004_agent_link_host.sql", "0005_agent_network.sql"} {
		path := "migrations/control/" + name
		data, err := migrationFiles.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		old[path] = &fstest.MapFile{Data: data}
	}
	if err := migrateDatabase(ctx, db, ControlDatabase, old); err != nil {
		t.Fatal(err)
	}
	execMigrationSQL(t, db, `INSERT INTO agent_link_host VALUES(1,'127.0.0.1:1234','https://retained.example','https')`)
	for range 2 {
		if err := Migrate(ctx, db, ControlDatabase); err != nil {
			t.Fatal(err)
		}
	}
	var endpoint, mode string
	if err := db.QueryRow(`SELECT endpoint,mode FROM agent_link_host`).Scan(&endpoint, &mode); err != nil {
		t.Fatal(err)
	}
	if endpoint != "" || mode != "address" {
		t.Fatal(endpoint, mode)
	}
}
