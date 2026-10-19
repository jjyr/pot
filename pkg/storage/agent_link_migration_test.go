package storage

import (
	"context"
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
