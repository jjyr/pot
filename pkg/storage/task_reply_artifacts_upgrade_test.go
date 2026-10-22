package storage

import (
	"context"
	"encoding/json"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

func TestTaskReplyArtifactsUpgrade(t *testing.T) {
	for _, skip := range []bool{false, true} {
		t.Run(map[bool]string{false: "previous", true: "skip-versions"}[skip], func(t *testing.T) {
			db := migrationDB(t)
			ctx := context.Background()
			prior := fstest.MapFS{}
			entries, _ := fs.ReadDir(migrationFiles, "migrations/tasks")
			for _, entry := range entries {
				if entry.Name() >= "0022_" || skip && entry.Name() >= "0008_" {
					continue
				}
				name := "migrations/tasks/" + entry.Name()
				data, _ := fs.ReadFile(migrationFiles, name)
				prior[name] = &fstest.MapFile{Data: data}
			}
			if err := migrateDatabase(ctx, db, TasksDatabase, prior); err != nil {
				t.Fatal(err)
			}
			for _, policy := range []string{"changes", "all", "failures"} {
				info := map[string]any{"id": policy, "spec": map[string]any{"title": "Monitor", "goal": "Keep original goal", "kind": "interval", "every_minutes": 60, "notify": policy, "timezone": "UTC", "machine_id": "original", "misfire": "skip"}, "next_at_ms": 123, "baseline": "retired", "created_at_ms": 456}
				body, _ := json.Marshal(map[string]any{"info": info, "generation": 7})
				reply, _ := json.Marshal(info)
				run, _ := json.Marshal(map[string]any{"id": policy, "generation": 7, "task_id": "original-task", "state": "running", "notify": policy, "changed": true})
				if _, err := db.Exec("INSERT INTO schedules VALUES('owner',?,?)", policy, body); err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec("INSERT INTO schedule_writes VALUES('owner',?,'original-digest',?)", policy, reply); err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec("INSERT INTO schedule_runs VALUES('owner',?,?,123,NULL,?)", policy, policy, run); err != nil {
					t.Fatal(err)
				}
			}
			envelope := `{"kind":"task_result","text":"Original final reply","result":{"artifacts":["report.txt"],"evidence":["original-source"]}}`
			if _, err := db.Exec(`INSERT INTO agent_messages(id,envelope,direction,state,conversation,recipient,detail) VALUES('outcome',?,'incoming','processing','conversation','owner','original')`, envelope); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if err := Migrate(ctx, db, TasksDatabase); err != nil {
					t.Fatal(err)
				}
				for _, policy := range []string{"changes", "all", "failures"} {
					wantPolicy := policy
					if policy == "changes" {
						wantPolicy = "all"
					}
					var body, reply, digest, runBody string
					if err := db.QueryRow("SELECT body FROM schedules WHERE id=?", policy).Scan(&body); err != nil {
						t.Fatal(err)
					}
					var plan struct {
						Info       protocol.ScheduleInfo `json:"info"`
						Generation uint64                `json:"generation"`
					}
					if err := core.DecodeJSON([]byte(body), &plan); err != nil {
						t.Fatal(err)
					}
					if plan.Generation != 7 || plan.Info.Spec.Notify != wantPolicy || plan.Info.Spec.Goal != "Keep original goal" || plan.Info.Spec.MachineID != "original" || *plan.Info.NextAtMS != 123 || strings.Contains(body, "baseline") {
						t.Fatal(body)
					}
					if err := db.QueryRow("SELECT digest,reply FROM schedule_writes WHERE request_id=?", policy).Scan(&digest, &reply); err != nil {
						t.Fatal(err)
					}
					var saved protocol.ScheduleInfo
					if err := core.DecodeJSON([]byte(reply), &saved); err != nil {
						t.Fatal(err)
					}
					if digest != "original-digest" || saved.Spec.Notify != wantPolicy || strings.Contains(reply, "baseline") {
						t.Fatal(digest, reply)
					}
					if err := db.QueryRow("SELECT body FROM schedule_runs WHERE id=?", policy).Scan(&runBody); err != nil {
						t.Fatal(err)
					}
					var run protocol.ScheduleRun
					if err := core.DecodeJSON([]byte(runBody), &run); err != nil {
						t.Fatal(err)
					}
					if run.Notify != wantPolicy || run.State != "running" || *run.TaskID != "original-task" || run.Generation != 7 || run.FinishedAtMS != nil || strings.Contains(runBody, "changed") {
						t.Fatal(runBody)
					}
				}
				var actual, state string
				if err := db.QueryRow("SELECT envelope,state FROM agent_messages WHERE id='outcome'").Scan(&actual, &state); err != nil {
					t.Fatal(err)
				}
				if actual != envelope || state != "processing" {
					t.Fatal("changed accepted outcome", actual, state)
				}
			}
		})
	}
}
