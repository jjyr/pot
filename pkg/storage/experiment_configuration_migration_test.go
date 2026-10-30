package storage

import (
	"path/filepath"
	"testing"
)

func TestExperimentConfigurationMigration(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "tasks.sqlite")
	db, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	paths := taskDefaultMigrationPaths(t, TasksDatabase)
	prior := taskDefaultBefore(paths, "migrations/tasks/0015_experiment_configuration.sql")
	migrateTaskDefaultPaths(t, db, TasksDatabase, prior)
	if err = Migrate(ctx, db, TasksDatabase); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO experiment_requests VALUES('owner','turn','hash','{}',NULL)"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO experiment_task_requests VALUES('owner','task-request','turn')"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, err = OpenDatabase(path, TasksDatabase)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var root string
	if err = db.QueryRow("SELECT experiment_id FROM experiment_task_requests WHERE assistant_id='owner' AND request_id='task-request'").Scan(&root); err != nil || root != "turn" {
		t.Fatal(root, err)
	}
	if _, err = db.Exec("INSERT INTO experiment_task_requests VALUES('other','task-request','turn')"); err == nil {
		t.Fatal("cross-owner experiment binding accepted")
	}
}
