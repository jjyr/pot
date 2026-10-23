package storage

import (
	"context"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

func TestScheduleBeforeRunUpgradePreservesStateAndReceipts(t *testing.T) {
	db := migrationDB(t)
	ctx := context.Background()
	old := fstest.MapFS{}
	entries, err := fs.ReadDir(migrationFiles, "migrations/tasks")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() >= "0028_" {
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
	plan := `{"info":{"id":"plan","spec":{"machine_id":"machine","goal":"saved goal","kind":"interval","every_minutes":90,"enabled":true},"next_at_ms":123456},"generation":8}`
	run := `{"id":"occurrence","task_id":"task","state":"running","generation":7,"notify":"all"}`
	execMigrationSQL(t, db, `INSERT INTO schedules VALUES('owner','plan','`+plan+`'); INSERT INTO schedule_runs VALUES('owner','plan','occurrence',123,NULL,'`+run+`'); INSERT INTO schedule_writes VALUES('owner','write','immutable-digest','{"id":"plan","spec":{"goal":"saved goal"}}');`)
	for range 2 {
		if err = Migrate(ctx, db, TasksDatabase); err != nil {
			t.Fatal(err)
		}
		var body string
		if err = db.QueryRow("SELECT body FROM schedules").Scan(&body); err != nil {
			t.Fatal(err)
		}
		if strings.Replace(body, `,"before_run":null`, "", 1) != plan {
			t.Fatal("plan overwritten", body)
		}
		if err = db.QueryRow("SELECT body FROM schedule_runs").Scan(&body); err != nil || strings.Replace(body, `,"hook":null`, "", 1) != run {
			t.Fatal("history changed", body, err)
		}
		var digest, reply string
		if err = db.QueryRow("SELECT digest,reply FROM schedule_writes").Scan(&digest, &reply); err != nil || digest != "immutable-digest" || !strings.Contains(reply, `"before_run":null`) {
			t.Fatal(digest, reply, err)
		}
		var count int
		if err = db.QueryRow("SELECT count(*) FROM schedule_probes").Scan(&count); err != nil || count != 0 {
			t.Fatal(count, err)
		}
	}
}
