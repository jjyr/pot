package storage

import (
	"context"
	"io/fs"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestAgentLinkDirectChatUpgradePreservesEstablishedNodesAndHistory(t *testing.T) {
	ctx := context.Background()
	db, err := OpenSQLite(filepath.Join(t.TempDir(), "control.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	old := fstest.MapFS{}
	if err = fs.WalkDir(migrationFiles, "migrations/control", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && entry.Name() < "0020_" {
			raw, err := migrationFiles.ReadFile(path)
			if err != nil {
				return err
			}
			old[path] = &fstest.MapFile{Data: raw}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err = migrateDatabase(ctx, db, ControlDatabase, old); err != nil {
		t.Fatal(err)
	}
	execMigrationSQL(t, db, `INSERT INTO agent_link_connections VALUES('established','node-route','ready','{"schema_version":4,"id":"established","state":"ready","last_seen":"2026-10-20T00:00:00Z","local":{"address":{"node_id":"local","kind":"assistant","agent_id":"deleted"}},"peer":{"address":{"node_id":"remote","kind":"assistant","agent_id":"main"}}}','saved-secret','saved-key');
 INSERT INTO agent_link_connections VALUES('pending','pending-route','connecting','{"schema_version":4,"state":"connecting"}','pending-secret','pending-key');
 INSERT INTO agent_link_chat(id,link_id,assistant_id,request_id,role,text,created_at,sender)VALUES('original','established','main','request','user','keep history',100,'deleted');
 INSERT INTO agent_link_invitations VALUES('old-invite','{}','tcp://127.0.0.1:8793',9999999999999,'invite-secret','','');
 CREATE TRIGGER fail_direct_chat_upgrade BEFORE UPDATE ON agent_link_connections BEGIN SELECT RAISE(ABORT,'interrupted upgrade'); END;`)
	count := migrationCount(t, db)
	if err = Migrate(ctx, db, ControlDatabase); err == nil {
		t.Fatal("failed migration accepted")
	}
	if migrationCount(t, db) != count {
		t.Fatal("failed migration persisted receipt")
	}
	execMigrationSQL(t, db, "DROP TRIGGER fail_direct_chat_upgrade")
	for range 2 {
		if err = Migrate(ctx, db, ControlDatabase); err != nil {
			t.Fatal(err)
		}
	}
	var version int
	var local, remote, sender, kind, text, secret, key string
	err = db.QueryRow(`SELECT json_extract(metadata,'$.schema_version'),json_extract(metadata,'$.local.address.kind'),json_extract(metadata,'$.peer.address.agent_id'),secret_ref,peer_key FROM agent_link_connections WHERE id='established'`).Scan(&version, &local, &remote, &secret, &key)
	if err != nil || version != 5 || local != "node" || remote != "remote" || secret != "saved-secret" || key != "saved-key" {
		t.Fatal(version, local, remote, secret, key, err)
	}
	err = db.QueryRow(`SELECT sender,sender_kind,text FROM agent_link_chat WHERE id='original'`).Scan(&sender, &kind, &text)
	if err != nil || sender != "deleted" || kind != "assistant" || text != "keep history" {
		t.Fatal(sender, kind, text, err)
	}
	var pending string
	err = db.QueryRow(`SELECT json_extract(metadata,'$.last_error') FROM agent_link_connections WHERE id='pending'`).Scan(&pending)
	if err != nil || pending != "agent_link_reconnect_required" {
		t.Fatal(pending, err)
	}
	var expiry, sessions int
	err = db.QueryRow(`SELECT expires_at FROM agent_link_invitations WHERE id='old-invite'`).Scan(&expiry)
	if err != nil || expiry != 0 {
		t.Fatal(expiry, err)
	}
	err = db.QueryRow(`SELECT COUNT(*) FROM agent_link_chat_sessions`).Scan(&sessions)
	if err != nil || sessions != 0 {
		t.Fatal("upgrade created or replayed conversation", sessions, err)
	}
}
