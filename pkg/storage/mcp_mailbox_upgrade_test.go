package storage

import (
	"encoding/json"
	"io/fs"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestMCPMailboxUpgradePreservesPendingAndConsumedHistory(t *testing.T) {
	for _, start := range []string{"0005", "0011"} {
		t.Run(start, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "tasks.sqlite")
			db, err := OpenSQLite(path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			prior := fstest.MapFS{}
			entries, err := fs.ReadDir(migrationFiles, "migrations/tasks")
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range entries {
				if e.Name()[:4] <= start {
					name := "migrations/tasks/" + e.Name()
					raw, e := fs.ReadFile(migrationFiles, name)
					if e != nil {
						t.Fatal(e)
					}
					prior[name] = &fstest.MapFile{Data: raw}
				}
			}
			if err = migrateDatabase(t.Context(), db, TasksDatabase, prior); err != nil {
				t.Fatal(err)
			}
			// Skipped-version upgrades may create the receiver journal afterward; service
			// receipts exist at 0005 and must retain the same acceptance and run identity.
			task := `{"id":"dispatch","run_id":"old-run","state":"complete"}`
			if _, err = db.Exec(`INSERT INTO mcp_service_tasks(id,connection_id,actor,request_id,digest,submission,task) VALUES('dispatch','caller','actor','submit','digest','{}',?)`, task); err != nil {
				t.Fatal(err)
			}
			binding := `{"task":{"node_id":"native","kind":"task","agent_id":"job"},"run_id":"new-run","receiver":{"node_id":"native","kind":"node","agent_id":"native"}}`
			admission := `{"binding":` + binding + `}`
			for _, id := range []string{"pending", "consumed"} {
				control := `{"delivery_id":"` + id + `","binding":{"task":{"agent_id":"job"},"run_id":"old-run"},"input":{"request_id":"` + id + `"}}`
				savedAdmission := ""
				if id == "consumed" {
					savedAdmission = admission
				}
				if _, err = db.Exec(`INSERT INTO mcp_service_deliveries(id,task_id,receiver_connection,receiver_address,control,claim,admission) VALUES(?,'dispatch','receiver','{}',?,'{}',?)`, id, control, savedAdmission); err != nil {
					t.Fatal(err)
				}
				receipt := `{"task":{"state":"complete","run_id":"old-run"}}`
				if _, err = db.Exec(`INSERT INTO mcp_service_mutations VALUES('caller','actor','task.input',?,'original-digest',?)`, id, receipt); err != nil {
					t.Fatal(err)
				}
				claimReceipt := `{"claim":{"delivery_id":"` + id + `"},"control":` + control + `}`
				if _, err = db.Exec(`INSERT INTO mcp_service_claim_requests VALUES('receiver',?,?)`, id, claimReceipt); err != nil {
					t.Fatal(err)
				}
				if start == "0011" {
					body := `{"schema_version":2,"claim":{},"control":` + control + `}`
					if id == "consumed" {
						body = `{"schema_version":2,"claim":{},"control":` + control + `,"admission":` + admission + `}`
					}
					if _, err = db.Exec(`INSERT INTO mcp_receiver_journal(connection_id,delivery_id,claim_id,body) VALUES('receiver',?,?,?)`, id, id, body); err != nil {
						t.Fatal(err)
					}
				}
			}
			if start == "0011" {
				if _, err = db.Exec(`INSERT INTO mcp_receiver_journal(connection_id,delivery_id,claim_id,body,execution_task,execution_run) VALUES('receiver','original','original','{"schema_version":2,"last_phase":"complete"}','job','old-run')`); err != nil {
					t.Fatal(err)
				}
				if _, err = db.Exec(`INSERT INTO retired_agent_link_work VALUES('job','retired-job')`); err != nil {
					t.Fatal(err)
				}
			}
			if err = Migrate(t.Context(), db, TasksDatabase); err != nil {
				t.Fatal(err)
			}
			for _, id := range []string{"pending", "consumed"} {
				var phase, disposition, claimPhase, receipt string
				if err = db.QueryRow(`SELECT json_extract(control,'$.input_phase'),input_receipt FROM mcp_service_deliveries WHERE id=?`, id).Scan(&phase, &disposition); err != nil || phase != "complete" {
					t.Fatal(phase, err)
				}
				if id == "pending" && disposition != "" {
					t.Fatal("pending input marked consumed")
				}
				if id == "consumed" {
					var r map[string]any
					if json.Unmarshal([]byte(disposition), &r) != nil || r["state"] != "consumed" {
						t.Fatal(disposition)
					}
				}
				if err = db.QueryRow(`SELECT json_extract(receipt,'$.control.input_phase') FROM mcp_service_claim_requests WHERE request_id=?`, id).Scan(&claimPhase); err != nil || claimPhase != phase {
					t.Fatal("lost claim retry changed", err)
				}
				if err = db.QueryRow(`SELECT receipt FROM mcp_service_mutations WHERE request_id=?`, id).Scan(&receipt); err != nil || receipt != `{"task":{"state":"complete","run_id":"old-run"}}` {
					t.Fatal("accepted receipt changed", receipt, err)
				}
				if start == "0011" {
					var body string
					if err = db.QueryRow(`SELECT body FROM mcp_receiver_journal WHERE delivery_id=?`, id).Scan(&body); err != nil {
						t.Fatal(err)
					}
					var r struct {
						Control struct {
							InputPhase string `json:"input_phase"`
						}
						InputReceipt *struct{ State string } `json:"input_receipt"`
					}
					if json.Unmarshal([]byte(body), &r) != nil || r.Control.InputPhase != "complete" {
						t.Fatal(body)
					}
					if (r.InputReceipt != nil) != (id == "consumed") {
						t.Fatal("receiver replay disposition changed", body)
					}
				}
			}
			var saved string
			if err = db.QueryRow(`SELECT task FROM mcp_service_tasks`).Scan(&saved); err != nil || saved != task {
				t.Fatal("execution identity changed", saved, err)
			}
			if err = db.Close(); err != nil {
				t.Fatal(err)
			}
			db, err = OpenDatabase(path, TasksDatabase)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if err = Migrate(t.Context(), db, TasksDatabase); err != nil {
				t.Fatal(err)
			}
			if start == "0011" {
				var n int
				if err = db.QueryRow(`SELECT count(*) FROM retired_agent_link_work WHERE id='retired-job'`).Scan(&n); err != nil || n != 1 {
					t.Fatal("retired fence changed", n, err)
				}
			}
		})
	}
}
