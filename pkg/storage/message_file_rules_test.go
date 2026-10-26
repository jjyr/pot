package storage

import (
	"fmt"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestMessageFileRulesUpgradePreservesSourcesStatesAndResources(t *testing.T) {
	for _, preceding := range []int{2, 3} {
		t.Run(fmt.Sprint(preceding), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "control.sqlite")
			db, err := OpenSQLite(path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			files := fstest.MapFS{}
			for _, name := range []string{"0001_initial_schema.sql", "0002_message_files.sql", "0003_agent_adapter.sql"}[:preceding] {
				name = "migrations/control/" + name
				data, err := migrationFiles.ReadFile(name)
				if err != nil {
					t.Fatal(err)
				}
				files[name] = &fstest.MapFile{Data: data}
			}
			if err := migrateDatabase(t.Context(), db, ControlDatabase, files); err != nil {
				t.Fatal(err)
			}
			// Exact serialized sources must survive: their bytes/decoded fields
			// contribute to existing idempotent resource receipt identities.
			const source = `{"node":"local","owner":"main","root":"/synthetic/workspace","root_id":"original"}`
			for i, state := range []string{"forbidden", "pending", "resolving", "ready"} {
				_, err := db.Exec(`INSERT INTO message_files VALUES('main','main',?,'revision',?,0,12,'/external/file','File',1,?,?,?)`, fmt.Sprint(i), fmt.Sprint(i), source, state, `{"id":"saved"}`)
				if err != nil {
					t.Fatal(err)
				}
			}
			execMigrationSQL(t, db, `INSERT INTO display_resources VALUES('saved','main','original-request','original-hash','{"id":"saved"}',0,1)`)
			if err := Migrate(t.Context(), db, ControlDatabase); err != nil {
				t.Fatal(err)
			}
			db.Close()
			db, err = OpenDatabase(path, ControlDatabase)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			for i, want := range []string{"forbidden", "pending", "resolving", "ready"} {
				var raw, state, resource string
				if err := db.QueryRow(`SELECT source,state,resource FROM message_files WHERE ref=?`, fmt.Sprint(i)).Scan(&raw, &state, &resource); err != nil {
					t.Fatal(err)
				}
				if raw != source || state != want || resource != `{"id":"saved"}` {
					t.Fatal("upgrade changed delivery authorization or result", raw, state, resource)
				}
			}
			var request, hash string
			if err := db.QueryRow(`SELECT request_id,input_hash FROM display_resources WHERE id='saved'`).Scan(&request, &hash); err != nil || request != "original-request" || hash != "original-hash" {
				t.Fatal("upgrade changed immutable receipt", request, hash, err)
			}
		})
	}
}
