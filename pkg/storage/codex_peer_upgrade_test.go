package storage

import (
	"context"
	"io/fs"
	"testing"
	"testing/fstest"
)

func TestCodexPeerUpgradeRetainsIdentityAndInvalidatesOldAddress(t *testing.T) {
	ctx := context.Background()
	db := migrationDB(t)
	prior := fstest.MapFS{}
	if err := fs.WalkDir(migrationFiles, "migrations/control", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && entry.Name() < "0008_" {
			raw, e := fs.ReadFile(migrationFiles, path)
			if e != nil {
				return e
			}
			prior[path] = &fstest.MapFile{Data: raw}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := migrateDatabase(ctx, db, ControlDatabase, prior); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO codex_agent_host VALUES(1,'stable-host','encrypted-owner','http://old-vpn:8793')"); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, db, ControlDatabase); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, db, ControlDatabase); err != nil {
		t.Fatal(err)
	}
	var id, owner, endpoint, public, secret string
	if err := db.QueryRow("SELECT node_id,owner_secret_ref,endpoint,peer_public_key,peer_secret_ref FROM codex_agent_host").Scan(&id, &owner, &endpoint, &public, &secret); err != nil {
		t.Fatal(err)
	}
	if id != "stable-host" || owner != "encrypted-owner" || endpoint != "" || public != "" || secret != "" {
		t.Fatal("migration changed identity or invented a new peer key")
	}
	if _, err := db.Exec("UPDATE codex_agent_host SET peer_public_key='persisted-public',peer_secret_ref='encrypted-key'"); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, db, ControlDatabase); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT peer_public_key,peer_secret_ref FROM codex_agent_host").Scan(&public, &secret); err != nil || public != "persisted-public" || secret != "encrypted-key" {
		t.Fatal("restart reset initialized identity", err)
	}
}
