package storage

import (
	"context"
	"encoding/json"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/jjyr/pot/pkg/protocol"
)

func TestCronSchedulesUpgrade(t *testing.T) {
	for _, skip := range []bool{false, true} {
		t.Run(map[bool]string{false: "previous", true: "skip-versions"}[skip], func(t *testing.T) {
			db := migrationDB(t)
			ctx := context.Background()
			old := fstest.MapFS{}
			entries, err := fs.ReadDir(migrationFiles, "migrations/tasks")
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if entry.Name() >= "0008_" || (skip && !strings.HasPrefix(entry.Name(), "0001_")) {
					continue
				}
				path := "migrations/tasks/" + entry.Name()
				raw, err := fs.ReadFile(migrationFiles, path)
				if err != nil {
					t.Fatal(err)
				}
				old[path] = &fstest.MapFile{Data: raw}
			}
			if err = migrateDatabase(ctx, db, TasksDatabase, old); err != nil {
				t.Fatal(err)
			}
			for _, kind := range []string{"once", "interval", "daily", "weekly"} {
				spec := map[string]any{"title": "Preserve", "goal": "Monitor", "machine_id": "remote", "timezone": "America/New_York", "kind": kind, "at": "2027-01-01 08:00", "every_minutes": 90, "time": "08:05", "weekdays": []int{1, 5, 7}, "enabled": kind != "once", "profile": "profile", "model": "model", "misfire": "skip", "notify": "all", "requirements": map[string]bool{"tools": true}}
				info := map[string]any{"id": kind, "spec": spec, "next_at_ms": 123456789, "baseline": "fingerprint", "created_at_ms": 123, "deleted": false, "last_error": "retained"}
				body, _ := json.Marshal(map[string]any{"info": info, "generation": 7})
				reply, _ := json.Marshal(info)
				if _, err = db.Exec("INSERT INTO schedules VALUES('owner',?,?)", kind, string(body)); err != nil {
					t.Fatal(err)
				}
				if _, err = db.Exec("INSERT INTO schedule_writes VALUES('owner',?,'original-digest',?)", kind, string(reply)); err != nil {
					t.Fatal(err)
				}
			}
			taskJSON := `{"schema_version":10,"state":"running"}`
			if skip {
				taskJSON = `{"schema_version":8,"state":"running"}`
			}
			if _, err = db.Exec("INSERT INTO tasks VALUES('owner','task','running',123,?)", taskJSON); err != nil {
				t.Fatal(err)
			}
			execMigrationSQL(t, db, `INSERT INTO schedule_runs VALUES('owner','daily','occurrence',123,NULL,'{"id":"occurrence","generation":7,"state":"running","task_id":"task"}'); INSERT INTO schedule_writes VALUES('owner','run','run-digest','{"task_id":"task"}');`)
			for range 2 {
				if err = Migrate(ctx, db, TasksDatabase); err != nil {
					t.Fatal(err)
				}
				for _, kind := range []string{"once", "interval", "daily", "weekly"} {
					var body, reply, digest string
					if err = db.QueryRow("SELECT body FROM schedules WHERE id=?", kind).Scan(&body); err != nil {
						t.Fatal(err)
					}
					var plan struct {
						Info       protocol.ScheduleInfo `json:"info"`
						Generation int                   `json:"generation"`
					}
					if err = json.Unmarshal([]byte(body), &plan); err != nil {
						t.Fatal(err)
					}
					info := plan.Info
					if plan.Generation != 7 || info.NextAtMS == nil || *info.NextAtMS != 123456789 || info.Spec.Timezone != "America/New_York" || info.Spec.MachineID != "remote" || *info.Spec.Profile != "profile" || *info.Spec.Model != "model" || info.Spec.Enabled != (kind != "once") || info.Spec.Misfire != "skip" || info.Spec.Notify != "all" || !info.Spec.Requirements.Tools || info.LastError != "retained" {
						t.Fatal("changed state", body)
					}
					wantKind := kind
					wantCron := ""
					if kind == "daily" {
						wantKind = "cron"
						wantCron = "5 8 * * *"
					}
					if kind == "weekly" {
						wantKind = "cron"
						wantCron = "5 8 * * 1,5,0"
					}
					if info.Spec.Kind != wantKind || info.Spec.Cron != wantCron || info.Spec.EveryMinutes != 90 || strings.Contains(body, `"weekdays"`) || strings.Contains(body, `"time"`) {
						t.Fatal(body)
					}
					if err = db.QueryRow("SELECT digest,reply FROM schedule_writes WHERE request_id=?", kind).Scan(&digest, &reply); err != nil {
						t.Fatal(err)
					}
					var saved protocol.ScheduleInfo
					if err = json.Unmarshal([]byte(reply), &saved); err != nil {
						t.Fatal(err)
					}
					if digest != "original-digest" || saved.Spec.Kind != wantKind || saved.Spec.Cron != wantCron {
						t.Fatal(digest, reply)
					}
				}
				var taskBody, receipt, receiptDigest string
				if err = db.QueryRow("SELECT body FROM tasks WHERE id='task'").Scan(&taskBody); err != nil {
					t.Fatal(taskBody, err)
				}
				var taskRecord map[string]any
				if err = json.Unmarshal([]byte(taskBody), &taskRecord); err != nil || taskRecord["schema_version"] != float64(13) || taskRecord["state"] != "running" || (!skip && taskBody != strings.Replace(taskJSON, `"schema_version":10`, `"schema_version":13`, 1)) {
					t.Fatal("migration changed active Task", taskBody, err)
				}
				if err = db.QueryRow("SELECT digest,reply FROM schedule_writes WHERE request_id='run'").Scan(&receiptDigest, &receipt); err != nil || receiptDigest != "run-digest" || receipt != `{"task_id":"task"}` {
					t.Fatal(receipt, receiptDigest, err)
				}
				var run string
				if err = db.QueryRow("SELECT body FROM schedule_runs").Scan(&run); err != nil || run != `{"id":"occurrence","generation":7,"state":"running","task_id":"task","hook":null}` {
					t.Fatal(run, err)
				}
			}
		})
	}
}
