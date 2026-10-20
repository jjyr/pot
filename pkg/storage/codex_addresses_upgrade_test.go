package storage

import (
	"context"
	"io/fs"
	"testing"
	"testing/fstest"
)

func TestCodexAddressesUpgradePreservesOwnerAndPriorEndpoint(t *testing.T) {
	db := migrationDB(t)
	ctx := context.Background()
	prior := fstest.MapFS{}
	entries, _ := fs.ReadDir(migrationFiles, "migrations/control")
	for _, entry := range entries {
		if entry.Name() >= "0019_" {
			continue
		}
		path := "migrations/control/" + entry.Name()
		raw, _ := fs.ReadFile(migrationFiles, path)
		prior[path] = &fstest.MapFile{Data: raw}
	}
	if err := migrateDatabase(ctx, db, ControlDatabase, prior); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO codex_agent_host VALUES(1,'stable-node','owner-ref','tcp://100.88.0.4:8793','public-key','key-ref')"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := Migrate(ctx, db, ControlDatabase); err != nil {
			t.Fatal(err)
		}
	}
	var node, owner, endpoint, pub, key string
	if err := db.QueryRow("SELECT node_id,owner_secret_ref,endpoint,peer_public_key,peer_secret_ref FROM codex_agent_host").Scan(&node, &owner, &endpoint, &pub, &key); err != nil || node != "stable-node" || owner != "owner-ref" || endpoint != "tcp://100.88.0.4:8793" || pub != "public-key" || key != "key-ref" {
		t.Fatal(node, owner, endpoint, pub, key, err)
	}
	for _, address := range []string{"tcp://127.0.0.1:8793", "tcp://192.168.1.4:8793"} {
		if _, err := db.Exec("INSERT INTO codex_agent_endpoints VALUES(?)", address); err != nil {
			t.Fatal(err)
		}
	}
	if err := Migrate(ctx, db, ControlDatabase); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM codex_agent_endpoints").Scan(&count); err != nil || count != 2 {
		t.Fatal(count, err)
	}
}
