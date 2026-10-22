package storage

import (
	"context"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestAgentLinkNodeUpgradeRequiresExplicitAuthorityWithoutReplayingWork(t *testing.T) {
	ctx := context.Background()
	db, e := OpenSQLite(filepath.Join(t.TempDir(), "control.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	old := fstest.MapFS{}
	fs.WalkDir(migrationFiles, "migrations/control", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && entry.Name() < "0015_" {
			raw, e := migrationFiles.ReadFile(path)
			if e != nil {
				return e
			}
			old[path] = &fstest.MapFile{Data: raw}
		}
		return nil
	})
	if e = migrateDatabase(ctx, db, ControlDatabase, old); e != nil {
		t.Fatal(e)
	}
	_, e = db.Exec(`INSERT INTO agent_link_connections(id,route,state,metadata,secret_ref,peer_key)VALUES('saved-id','saved-route','ready','{"schema_version":3,"id":"saved-id","state":"ready","local":{"address":{"node_id":"local","kind":"assistant","agent_id":"one"}},"peer":{"address":{"node_id":"remote","kind":"assistant","agent_id":"two"}}}','saved-secret','saved-key');INSERT INTO agent_link_invitations VALUES('invite','{}','tcp://127.0.0.1:12345',9999999999999,'invite-secret','','');`)
	if e != nil {
		t.Fatal(e)
	}
	for range 2 {
		if e = Migrate(ctx, db, ControlDatabase); e != nil {
			t.Fatal(e)
		}
	}
	var state, metadata, secret, key string
	if e = db.QueryRow("SELECT state,metadata,secret_ref,peer_key FROM agent_link_connections WHERE id='saved-id'").Scan(&state, &metadata, &secret, &key); e != nil {
		t.Fatal(e)
	}
	if state != "offline" || !strings.Contains(metadata, "agent_link_reconnect_required") || secret != "saved-secret" || key != "saved-key" {
		t.Fatal(state, metadata, secret, key)
	}
	var expiry, count int
	if e = db.QueryRow("SELECT expires_at FROM agent_link_invitations WHERE id='invite'").Scan(&expiry); e != nil || expiry != 0 {
		t.Fatal(expiry, e)
	}
	if e = db.QueryRow("SELECT COUNT(*) FROM retired_task_submissions").Scan(&count); e != nil || count != 0 {
		t.Fatal("migration launched delegation", count, e)
	}
}
