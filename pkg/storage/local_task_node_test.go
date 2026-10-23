package storage

import (
	"context"
	"testing"
)

func TestLocalTaskNodeUpgradePreservesOriginalAuthorityAndWork(t *testing.T) {
	ctx := context.Background()
	db := migrationDB(t)
	nodeMigrationPrefix(t, db, TasksDatabase, 21)
	const task = `{"schema_version":10,"run_id":"original-run","launch":{"control_id":"owner","runtime_id":"retired-runner"},"launch_sent":true}`
	const schedule = `{"machine_id":"retired-runner","enabled":true}`
	execMigrationSQL(t, db, "INSERT INTO task_authority VALUES('owner','old-runtime')")
	if _, err := db.Exec("INSERT INTO tasks VALUES('assistant','task','waiting',1,?)", task); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO schedules VALUES('assistant','schedule',?)", schedule); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := Migrate(ctx, db, TasksDatabase); err != nil {
			t.Fatal(err)
		}
		var node, coordinator, runtime, body, plan string
		if err := db.QueryRow("SELECT node_id FROM task_node WHERE singleton=1").Scan(&node); err != nil || node != "owner" {
			t.Fatal(node, err)
		}
		if err := db.QueryRow("SELECT control_id,runtime_id FROM retired_task_authority").Scan(&coordinator, &runtime); err != nil || coordinator != "owner" || runtime != "old-runtime" {
			t.Fatal(coordinator, runtime, err)
		}
		if err := db.QueryRow("SELECT body FROM tasks").Scan(&body); err != nil || body != `{"launch":{"control_id":"owner","runtime_id":"retired-runner"},"node_id":"retired-runner","run_id":"original-run","schema_version":13}` {
			t.Fatal("execution identity changed", body, err)
		}
		if err := db.QueryRow("SELECT body FROM schedules").Scan(&plan); err != nil || plan != schedule {
			t.Fatal("schedule was moved to another machine", plan, err)
		}
		var live int
		if err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='task_authority'").Scan(&live); err != nil || live != 0 {
			t.Fatal("distributed authority remains active", live, err)
		}
	}
}
