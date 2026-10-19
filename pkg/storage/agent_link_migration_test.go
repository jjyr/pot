package storage

import (
	"context"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestAgentLinkMigrationPreservesExistingControlData(t *testing.T) {
	ctx := context.Background()
	db := migrationDB(t)
	baseline, err := migrationFiles.ReadFile("migrations/control/0001_baseline.sql")
	if err != nil {
		t.Fatal(err)
	}
	old := fstest.MapFS{"migrations/control/0001_baseline.sql": &fstest.MapFile{Data: baseline}}
	if err = migrateDatabase(ctx, db, ControlDatabase, old); err != nil {
		t.Fatal(err)
	}
	execMigrationSQL(t, db, `INSERT INTO integration_accounts VALUES('github',3,'{"identity":{"id":"retained"}}','secret-ref'); INSERT INTO secrets VALUES('secret-ref',X'0102',X'0304',X'0506');`)
	for range 2 {
		if err = Migrate(ctx, db, ControlDatabase); err != nil {
			t.Fatal(err)
		}
	}
	var service, secretRef, metadata string
	var revision int
	if err = db.QueryRow(`SELECT service,revision,metadata,secret_ref FROM integration_accounts`).Scan(&service, &revision, &metadata, &secretRef); err != nil {
		t.Fatal(err)
	}
	if service != "github" || revision != 3 || secretRef != "secret-ref" || metadata != "{\"identity\":{\"id\":\"retained\"}}" {
		t.Fatal(service, revision, metadata, secretRef)
	}
	var count int
	if err = db.QueryRow(`SELECT COUNT(*) FROM secrets WHERE id='secret-ref' AND ciphertext=X'0304'`).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	for _, table := range []string{"agent_link_connections", "agent_link_invitations"} {
		exists, err := tableExists(ctx, db, table)
		if err != nil || !exists {
			t.Fatal(table, exists, err)
		}
	}
	definitions, err := loadMigrations(migrationFiles, ControlDatabase)
	if err != nil || migrationCount(t, db) != len(definitions) {
		t.Fatal(err)
	}
}

func TestAgentLinkTCPMigrationInvalidatesHTTPWithoutLosingMessageReceipts(t *testing.T) {
	ctx := context.Background()
	db := migrationDB(t)
	old := fstest.MapFS{}
	for _, name := range []string{"0001_baseline.sql", "0002_agent_links.sql"} {
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
	execMigrationSQL(t, db, `INSERT INTO secrets VALUES('old-link-secret',X'01',X'02',X'03'),('old-invite-secret',X'04',X'05',X'06'),('provider-secret',X'07',X'08',X'09');
INSERT INTO agent_link_connections VALUES('old-link','route','ready','{"id":"old-link","local":{"address":{"control_id":"a","kind":"assistant","agent_id":"main"},"name":"A","adapter":"pot"},"peer":{"address":{"control_id":"b","kind":"assistant","agent_id":"main"},"name":"B","adapter":"pot"},"endpoint":"http://100.100.1.2:8792","state":"ready","created_at":"2026-10-19T00:00:00Z","updated_at":"2026-10-19T00:00:00Z","last_seen":"2026-10-19T00:00:00Z","revocation_pending":true}','old-link-secret','old-token-hash');
INSERT INTO agent_link_connections VALUES('revoked-link','old-route','revoked','{"id":"revoked-link","state":"revoked","endpoint":"https://old.example","revocation_pending":true}','','old-revoked-hash');
INSERT INTO agent_link_invitations VALUES('old-invite','{}','https://old.example',9999999999999,'old-invite-secret','','');`)
	tasks, err := OpenDatabase(filepath.Join(t.TempDir(), "tasks.sqlite"), TasksDatabase)
	if err != nil {
		t.Fatal(err)
	}
	defer tasks.Close()
	if _, err = tasks.Exec(`INSERT INTO agent_messages(id,envelope,direction,state,recipient,conversation,detail)VALUES('retained-message','{"id":"retained-message"}','out','delivered','main','conversation','immutable receipt')`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = Migrate(ctx, db, ControlDatabase); err != nil {
			t.Fatal(err)
		}
	}
	var state, peerKey, secretRef, endpoint, lastError string
	var version, pending int
	if err = db.QueryRow(`SELECT state,peer_key,secret_ref,json_extract(metadata,'$.schema_version'),json_extract(metadata,'$.endpoint'),json_extract(metadata,'$.last_error'),json_extract(metadata,'$.revocation_pending') FROM agent_link_connections WHERE id='old-link'`).Scan(&state, &peerKey, &secretRef, &version, &endpoint, &lastError, &pending); err != nil {
		t.Fatal(err)
	}
	if state != "offline" || peerKey != "" || secretRef != "" || version != 2 || endpoint != "" || lastError != "agent_link_reconnect_required" || pending != 0 {
		t.Fatal(state, peerKey, secretRef, version, endpoint, lastError, pending)
	}
	if err = db.QueryRow(`SELECT state,json_extract(metadata,'$.last_error') FROM agent_link_connections WHERE id='revoked-link'`).Scan(&state, &lastError); err != nil || state != "revoked" || lastError != "" {
		t.Fatal(state, lastError, err)
	}
	var count int
	if err = db.QueryRow(`SELECT COUNT(*) FROM agent_link_invitations`).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	if err = db.QueryRow(`SELECT COUNT(*) FROM secrets`).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	if err = db.QueryRow(`SELECT COUNT(*) FROM secrets WHERE id='provider-secret' AND ciphertext=X'08'`).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	var receipt string
	if err = tasks.QueryRow(`SELECT state,detail FROM agent_messages WHERE id='retained-message'`).Scan(&state, &receipt); err != nil || state != "delivered" || receipt != "immutable receipt" {
		t.Fatal(state, receipt, err)
	}
}
