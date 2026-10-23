package storage

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/jjyr/pot/pkg/core"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalExtensionExportMovesPackagesAndRetiresSQL(t *testing.T) {
	root := t.TempDir()
	db, err := OpenSQLite(filepath.Join(root, "control.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	nodeMigrationPrefix(t, db, ControlDatabase, 27)
	execMigrationSQL(t, db, `INSERT INTO extension_settings VALUES('plugins','example',0,7,'/source'); INSERT INTO mcp_servers VALUES('example',4,'{"id":"example","name":"Example","transport":"stdio","command":"example","secret_ref":"encrypted-reference"}')`)
	for _, dir := range []string{"agent/plugins/example", "agent/skills/example", "skills"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	manifest := filepath.Join(root, "agent/plugins/example/plugin.json")
	if err := os.WriteFile(manifest, []byte(`{"schema_version":2,"id":"example","version":"1","contributes":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "agent/skills/example/SKILL.md"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(context.Background(), db, ControlDatabase); err != nil {
		t.Fatal(err)
	}
	if err := ExportLocalExtensions(db, root, root); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "plugins/example/plugin.json"))
	if err != nil {
		t.Fatal(err)
	}
	var definition map[string]any
	if json.Unmarshal(raw, &definition) != nil || definition["name"] != "example" || definition["schema_version"] != float64(3) || definition["id"] != nil {
		t.Fatal(string(raw))
	}
	raw, err = os.ReadFile(filepath.Join(root, "mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	var mcp struct {
		Servers []struct {
			SecretRef string `json:"secret_ref"`
			Revision  int64  `json:"revision"`
		} `json:"servers"`
	}
	if json.Unmarshal(raw, &mcp) != nil || len(mcp.Servers) != 1 || mcp.Servers[0].SecretRef != "encrypted-reference" || mcp.Servers[0].Revision != 4 {
		t.Fatal(string(raw))
	}
	for _, table := range []string{"retired_extension_settings", "retired_mcp_servers", "extension_file_export"} {
		var count int
		if err := db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatal(table, count, err)
		}
	}
	edited := []byte(`{"version":1,"plugins":{},"skills":{}}`)
	if err := os.WriteFile(filepath.Join(root, "extensions.json"), edited, 0600); err != nil {
		t.Fatal(err)
	}
	if err := ExportLocalExtensions(db, root, root); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(filepath.Join(root, "extensions.json"))
	if err != nil || string(raw) != string(edited) {
		t.Fatal("restart replaced file-owned configuration", string(raw), err)
	}
}
func TestLocalExtensionExportRejectsConflictsAndCanRetry(t *testing.T) {
	root := t.TempDir()
	db, err := OpenDatabase(filepath.Join(root, "control.sqlite"), ControlDatabase)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	execMigrationSQL(t, db, `INSERT INTO retired_extension_settings VALUES('plugins','example',0,1,'')`)
	path := filepath.Join(root, "extensions.json")
	original := []byte(`{"version":1,"plugins":{"local":{"enabled":true}},"skills":{}}`)
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := ExportLocalExtensions(db, root, root); err == nil {
		t.Fatal("overwrote conflicting configuration")
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != string(original) {
		t.Fatal("configuration modified")
	}
	var pending int
	db.QueryRow("SELECT count(*) FROM extension_file_export").Scan(&pending)
	if pending != 1 {
		t.Fatal("lost migration receipt")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := ExportLocalExtensions(db, root, root); err != nil {
		t.Fatal(err)
	}
}
func TestLocalRunsImportPreservesEvidenceAndExportsResourcesOnce(t *testing.T) {
	root := t.TempDir()
	source, err := OpenSQLite(filepath.Join(root, "runner.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	nodeMigrationPrefix(t, source, RunnerDatabase, 3)
	input := json.RawMessage(`{"request":{"id":"call"},"operation":{"kind":"shell","command":"original"}}`)
	digest, _ := core.HashJSON(input)
	resources, _ := json.Marshal(map[string]any{"roots": []any{map[string]any{"builtin": false, "files": map[string]string{"example/SKILL.md": base64.RawStdEncoding.EncodeToString([]byte("frozen skill"))}}}})
	resourceDigest, _ := core.HashJSON(json.RawMessage(resources))
	execMigrationSQL(t, source, `INSERT INTO task_runs VALUES('node','task','run','original-launch','running','old-boot','{"phase":"running","launch_digest":"original-launch"}');INSERT INTO task_events VALUES('node','task','run',1,'original-event');INSERT INTO run_workspaces VALUES('node','task','run','original-workspace')`)
	if _, err := source.Exec(`INSERT INTO run_commands VALUES('node','task','run','call',?,'complete','{"value":{"text":"original-result"},"cleanup_unconfirmed":false}')`, digest); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Exec(`INSERT INTO run_command_inputs VALUES('node','task','run','call',?)`, string(input)); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Exec(`INSERT INTO run_resources VALUES('node','task','run',?,?)`, resourceDigest, string(resources)); err != nil {
		t.Fatal(err)
	}
	source.Close()
	for range 2 {
		if err := ImportLocalRuns(root); err != nil {
			t.Fatal(err)
		}
	}
	db, err := OpenDatabase(filepath.Join(root, "tasks.sqlite"), TasksDatabase)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var phase, launch, event, workspace string
	if err := db.QueryRow("SELECT phase,digest FROM task_runs").Scan(&phase, &launch); err != nil || phase != "recovery_required" || launch != "original-launch" {
		t.Fatal(phase, launch, err)
	}
	db.QueryRow("SELECT event FROM task_events").Scan(&event)
	db.QueryRow("SELECT body FROM run_workspaces").Scan(&workspace)
	if event != "original-event" || workspace != "original-workspace" {
		t.Fatal(event, workspace)
	}
	receipts, err := ReadRetiredOperations(context.Background(), db, "node", "task", "run")
	if err != nil || len(receipts) != 1 || !receipts[0].Known || string(receipts[0].Result) != `{"text":"original-result"}` {
		t.Fatal(receipts, err)
	}
	raw, _, err := (ResourceFiles{filepath.Join(root, "cache/resources")}).Read("node", "task", "run")
	if err != nil {
		t.Fatal(err)
	}
	var snapshot struct {
		Roots []core.SkillRoot `json:"roots"`
	}
	if err := json.Unmarshal(raw, &snapshot); err != nil || len(snapshot.Roots) != 1 {
		t.Fatal(string(raw), err)
	}
	skill, err := os.ReadFile(filepath.Join(snapshot.Roots[0].Path, "example/SKILL.md"))
	if err != nil || string(skill) != "frozen skill" {
		t.Fatal(string(skill), err)
	}
	if _, err := db.Exec(`UPDATE retired_execution_receipts SET input='{}'`); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRetiredOperations(context.Background(), db, "node", "task", "run"); err == nil {
		t.Fatal("accepted damaged original evidence")
	}
}
