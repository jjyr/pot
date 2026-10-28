package storage

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestMCPCutoverUpgradeRetiresPairingPreservesSavedHistory(t *testing.T) {
	for _, start := range []string{"0004", "0005", "0008"} {
		t.Run(start, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "control.sqlite")
			db, e := OpenSQLite(path)
			if e != nil {
				t.Fatal(e)
			}
			defer db.Close()
			prior := fstest.MapFS{}
			entries, _ := fs.ReadDir(migrationFiles, "migrations/control")
			for _, entry := range entries {
				if entry.Name()[:4] > start {
					continue
				}
				name := "migrations/control/" + entry.Name()
				raw, _ := fs.ReadFile(migrationFiles, name)
				prior[name] = &fstest.MapFile{Data: raw}
			}
			if e = migrateDatabase(t.Context(), db, ControlDatabase, prior); e != nil {
				t.Fatal(e)
			}
			metadata := `{"schema_version":5,"peer_host":{"node_id":"original-node","public_key":"obsolete-pairing-key"},"id":"old-link","local":{"address":{"node_id":"local","kind":"node","agent_id":"local"},"name":"Pot","adapter":"pot"},"peer":{"address":{"node_id":"original-node","kind":"node","agent_id":"original-node"},"name":"Original","adapter":"codex"},"endpoint":"tcp://192.0.2.1:1","state":"connecting","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z","directory":{"node_id":"original-node","name":"Original","adapter":"codex","targets":[]}}`
			for _, statement := range []string{
				`INSERT INTO agent_link_connections VALUES('old-link','route','connecting',?,'obsolete-secret','obsolete-pairing-key')`,
			} {
				if _, e = db.Exec(statement, metadata); e != nil {
					t.Fatal(e)
				}
			}
			if _, e = db.Exec(`INSERT INTO agent_link_invitations(id,identity,endpoint,expires_at,secret_ref) VALUES('invite','{}','tcp://192.0.2.1:1',9999999999999,'obsolete-secret')`); e != nil {
				t.Fatal(e)
			}
			if _, e = db.Exec(`INSERT INTO agent_link_chat(id,link_id,assistant_id,request_id,role,text,created_at) VALUES('saved-chat','old-link','codex','request','assistant','Original saved report',123)`); e != nil {
				t.Fatal(e)
			}
			if _, e = db.Exec(`INSERT INTO agent_link_received_tasks VALUES('receipt','{"node_id":"original-node","kind":"assistant","agent_id":"codex"}','request','codex','{"text":"immutable request"}','saved-task')`); e != nil {
				t.Fatal(e)
			}
			if e = Migrate(t.Context(), db, ControlDatabase); e != nil {
				t.Fatal(e)
			}
			var raw string
			if e = db.QueryRow("SELECT metadata FROM agent_link_history WHERE id='old-link'").Scan(&raw); e != nil {
				t.Fatal(e)
			}
			var historical map[string]any
			if e = json.Unmarshal([]byte(raw), &historical); e != nil {
				t.Fatal(e)
			}
			if historical["schema_version"] != float64(1) || historical["state"] != "unsupported" || historical["last_known_state"] != "connecting" {
				t.Fatal(historical)
			}
			if strings.Contains(raw, "obsolete-secret") || strings.Contains(raw, "obsolete-pairing-key") || strings.Contains(raw, "peer_host") {
				t.Fatal("pairing authority retained in history", raw)
			}
			for _, table := range []string{"agent_link_connections", "agent_link_invitations", "agent_link_claim_digest_formats", "agent_link_host", "agent_link_endpoints", "agent_adapter_endpoints"} {
				var count int
				if e = db.QueryRow("SELECT count(*) FROM sqlite_schema WHERE name=?", table).Scan(&count); e != nil || count != 0 {
					t.Fatal("old executable transport schema retained", table, count, e)
				}
			}
			if e = db.QueryRow("SELECT text FROM agent_link_chat WHERE id='saved-chat'").Scan(&raw); e != nil || raw != "Original saved report" {
				t.Fatal(raw, e)
			}
			if e = db.QueryRow("SELECT payload FROM agent_link_received_tasks WHERE id='receipt'").Scan(&raw); e != nil || raw != `{"text":"immutable request"}` {
				t.Fatal(raw, e)
			}
			if e = Migrate(t.Context(), db, ControlDatabase); e != nil {
				t.Fatal(e)
			}
			db.Close()
			db, e = OpenDatabase(path, ControlDatabase)
			if e != nil {
				t.Fatal(e)
			}
			defer db.Close()
		})
	}
	for _, start := range []string{"0004", "0009"} {
		t.Run("tasks-"+start, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "tasks.sqlite")
			db, e := OpenSQLite(path)
			if e != nil {
				t.Fatal(e)
			}
			defer db.Close()
			prior := fstest.MapFS{}
			entries, _ := fs.ReadDir(migrationFiles, "migrations/tasks")
			for _, entry := range entries {
				if entry.Name()[:4] > start {
					continue
				}
				name := "migrations/tasks/" + entry.Name()
				raw, _ := fs.ReadFile(migrationFiles, name)
				prior[name] = &fstest.MapFile{Data: raw}
			}
			if e = migrateDatabase(t.Context(), db, TasksDatabase, prior); e != nil {
				t.Fatal(e)
			}
			payload := `{"schema_version":14,"info":{"id":"saved-task","phase":"running","agent_link":{"link_id":"old-link","node_id":"original-node"}},"execution":{"receipt":{"id":"original-task","run_id":"original-run","state":"running","result":"last confirmed report"}}}`
			if _, e = db.Exec(`INSERT INTO tasks VALUES('owner','saved-task','running',123,?)`, payload); e != nil {
				t.Fatal(e)
			}
			if _, e = db.Exec(`INSERT INTO task_run_history VALUES('owner','saved-task','original-run','{"report":"original saved report","artifact":"original-node/report.txt"}')`); e != nil {
				t.Fatal(e)
			}
			if _, e = db.Exec(`INSERT INTO retired_execution_receipts VALUES('original-node','original-task','original-run','command','digest','unknown','receipt','immutable-input')`); e != nil {
				t.Fatal(e)
			}
			saved := filepath.Join(root, "report.txt")
			bytes := []byte("original saved artifact bytes\n")
			if e = os.WriteFile(saved, bytes, 0600); e != nil {
				t.Fatal(e)
			}
			if e = Migrate(t.Context(), db, TasksDatabase); e != nil {
				t.Fatal(e)
			}
			var raw, state string
			if e = db.QueryRow("SELECT body,phase FROM tasks WHERE id='saved-task'").Scan(&raw, &state); e != nil || raw != payload || state != "running" {
				t.Fatal("unfinished work rewritten", raw, state, e)
			}
			if e = db.QueryRow("SELECT phase FROM retired_execution_receipts WHERE command_id='command'").Scan(&raw); e != nil || raw != "unknown" {
				t.Fatal(raw, e)
			}
			got, e := os.ReadFile(saved)
			if e != nil || string(got) != string(bytes) {
				t.Fatal("saved bytes changed", e)
			}
			if e = Migrate(t.Context(), db, TasksDatabase); e != nil {
				t.Fatal(e)
			}
			db.Close()
			db, e = OpenDatabase(path, TasksDatabase)
			if e != nil {
				t.Fatal(e)
			}
			defer db.Close()
			if e = db.QueryRow("SELECT body FROM tasks WHERE id='saved-task'").Scan(&raw); e != nil || raw != payload {
				t.Fatal(raw, e)
			}
		})
	}
}
