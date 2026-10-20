package storage

import (
	"context"
	"encoding/json"
	"testing"
	"testing/fstest"
)

func TestRunnerTCPUpgradeRetainsOriginalAuthorityAndJoinIntent(t *testing.T) {
	ctx := context.Background()
	db := migrationDB(t)
	path := "migrations/machines/0001_baseline.sql"
	data, err := migrationFiles.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = migrateDatabase(ctx, db, MachinesDatabase, fstest.MapFS{path: &fstest.MapFile{Data: data}}); err != nil {
		t.Fatal(err)
	}
	original := `{"version":1,"runtime_id":"runner","key":"stable-key","owner":"owner-secret","role":"runner","membership":{"origin":"https://old-owner.example","ca_pem":"old-ca","enrollment":{"control":{"runtime_id":"control","public_key":"stable-peer-key"},"epoch":9,"token":"member-secret","request_id":"original-join"}},"pending":{"origin":"http://old-owner.example","request":{"request_id":"uncertain-join","invitation":"original-invitation"},"control":{"runtime_id":"control","public_key":"stable-peer-key"}}}`
	if _, err = db.Exec(`INSERT INTO controlplane_state(id,body)VALUES(1,?)`, original); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = Migrate(ctx, db, MachinesDatabase); err != nil {
			t.Fatal(err)
		}
	}
	var raw string
	if err = db.QueryRow(`SELECT body FROM controlplane_state`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var current map[string]any
	if json.Unmarshal([]byte(raw), &current) != nil {
		t.Fatal(raw)
	}
	member := current["membership"].(map[string]any)
	pending := current["pending"].(map[string]any)
	enrollment := member["enrollment"].(map[string]any)
	if current["version"] != float64(2) || current["key"] != "stable-key" || member["origin"] != "" || pending["origin"] != "" || enrollment["epoch"] != float64(9) || enrollment["token"] != "member-secret" || pending["request"].(map[string]any)["request_id"] != "uncertain-join" {
		t.Fatal(raw)
	}
	if _, ok := member["ca_pem"]; ok {
		t.Fatal("obsolete TLS configuration retained", raw)
	}
}
