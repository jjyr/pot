package storage

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspaceIdentityUpgradeFencesLegacyWithoutRebinding(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.sqlite")
	db, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	nodeMigrationPrefix(t, db, TasksDatabase, 28)
	workspace := t.TempDir()
	file := filepath.Join(workspace, "result.txt")
	if err := os.WriteFile(file, []byte("retained original work"), 0600); err != nil {
		t.Fatal(err)
	}
	originals := map[string]map[string]any{}
	for _, kind := range []string{"temporary", "directory", "worktree", "preparing", "failed", "v2"} {
		status, identity := "ready", "old-device-inode-hash"
		if kind == "preparing" || kind == "failed" {
			status = kind
		}
		if kind == "v2" {
			identity = "directory-v2:retained-evidence"
		}
		body := map[string]any{"status": status, "kind": kind, "path": workspace, "directory_id": identity, "branch": "original-branch", "commit": "original-commit", "git_dirs": []any{"retained-git-dir"}}
		originals[kind] = body
		raw, _ := json.Marshal(body)
		if _, err := db.Exec("INSERT INTO run_workspaces VALUES('local',?,'original-run',?)", kind, string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec("INSERT INTO task_events VALUES('local','directory','original-run',1,'retained-event')"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := Migrate(context.Background(), db, TasksDatabase); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = OpenDatabase(path, TasksDatabase)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for kind, original := range originals {
		var raw string
		if err := db.QueryRow("SELECT body FROM run_workspaces WHERE control_id='local' AND task_id=? AND run_id='original-run'", kind).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		if err := json.Unmarshal([]byte(raw), &got); err != nil {
			t.Fatal(err)
		}
		if kind == "temporary" || kind == "directory" || kind == "worktree" {
			if got["status"] != "identity_unverified" || got["error"] == nil {
				t.Fatal("legacy identity was trusted", got)
			}
			delete(got, "error")
			got["status"] = "ready"
		}
		wantJSON, _ := json.Marshal(original)
		gotJSON, _ := json.Marshal(got)
		if string(wantJSON) != string(gotJSON) {
			t.Fatal("workspace history/evidence changed", string(wantJSON), string(gotJSON))
		}
	}
	var event string
	if err := db.QueryRow("SELECT event FROM task_events").Scan(&event); err != nil || event != "retained-event" {
		t.Fatal("run event changed", event, err)
	}
	if data, err := os.ReadFile(file); err != nil || string(data) != "retained original work" {
		t.Fatal("workspace files changed", string(data), err)
	}
}
