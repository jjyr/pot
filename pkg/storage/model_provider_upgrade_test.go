package storage

import (
	"context"
	"encoding/json"
	"io/fs"
	"reflect"
	"testing"
	"testing/fstest"
)

func TestModelProviderUpgradePreservesImmutableRoutes(t *testing.T) {
	ctx := context.Background()
	db := migrationDB(t)
	prior := fstest.MapFS{}
	entries, err := fs.ReadDir(migrationFiles, "migrations/control")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() >= "0015_" {
			continue
		}
		name := "migrations/control/" + entry.Name()
		raw, err := fs.ReadFile(migrationFiles, name)
		if err != nil {
			t.Fatal(err)
		}
		prior[name] = &fstest.MapFile{Data: raw}
	}
	if err = migrateDatabase(ctx, db, ControlDatabase, prior); err != nil {
		t.Fatal(err)
	}
	original := map[string]any{"id": "route", "api_provider": "private-catalog", "endpoint": "https://example.invalid/custom/route", "allowed_hosts": []any{"example.invalid"}, "protocol": "openai_chat", "secret_ref": "provider/original"}
	raw, _ := json.Marshal(original)
	if _, err = db.Exec("INSERT INTO providers VALUES(?,?)", "route", string(raw)); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = Migrate(ctx, db, ControlDatabase); err != nil {
			t.Fatal(err)
		}
	}
	var after string
	if err = db.QueryRow("SELECT metadata FROM providers WHERE id='route'").Scan(&after); err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err = json.Unmarshal([]byte(after), &value); err != nil {
		t.Fatal(err)
	}
	if value["type_id"] != "standard-model-providers/custom" {
		t.Fatal(value)
	}
	delete(value, "type_id")
	if !reflect.DeepEqual(value, original) {
		t.Fatal("route or secret identity changed", value, original)
	}
	if _, err = db.Exec("INSERT INTO provider_metadata VALUES(?,?,?,?)", "route", "list_models", "digest", `{"catalog":{"read_at":"2026-10-20T00:00:00Z","models":[]}}`); err != nil {
		t.Fatal(err)
	}
}
