package storage

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

func TestTaskTargetUpgradePinsReceiversAndFencesUnresolvedDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.sqlite")
	db, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	nodeMigrationPrefix(t, db, TasksDatabase, 25)
	execMigrationSQL(t, db, "INSERT INTO task_node VALUES(1,'local')")
	from := targetAssistant("local", "owner")
	for _, kind := range []string{"node", "assistant", "unresolved"} {
		request := legacyCreateTask{Executor: "", LinkID: "saved-link", RequestID: kind, Text: "任务 <tag> & \"quoted\"\n原始指令"}
		wire := executorUpgradeRequest{RequestID: kind, From: from, Text: request.Text}
		receipt := map[string]any{"id": "receiver-task-" + kind, "run_id": "run-" + kind, "state": "completed", "result": "saved result"}
		if kind == "node" {
			request.Executor = "agent"
			wire.Executor = "agent"
			receipt["execution"] = map[string]any{"address": protocol.AgentRef{NodeID: "remote", Kind: "task", AgentID: "receiver-task-node"}, "adapter": "fixture-agent", "name": "Agent"}
		}
		if kind == "assistant" {
			receipt["assistant"] = map[string]any{"address": targetAssistant("remote", "original-assistant"), "adapter": "fixture-agent", "name": "Assistant"}
		}
		if kind == "unresolved" {
			receipt = map[string]any{"state": "queued"}
		}
		body := map[string]any{"schema_version": 12, "assistant_id": "owner", "request": request, "origin": protocol.TaskOrigin{Kind: "manual"}, "run_id": "catalogue-run", "info": map[string]any{"id": kind, "assistant_id": "owner", "phase": "complete", "run_id": "catalogue-run", "archived": true, "agent_link": map[string]any{"node_id": "remote", "link_id": "saved-link", "kind": "fixture-agent"}}, "execution": map[string]any{"request": wire, "receipt": receipt, "notified": true, "stop_requested": true}, "result": map[string]any{"text": "saved result"}}
		raw, _ := json.Marshal(body)
		hash, _ := core.HashJSON([]any{request, protocol.TaskOrigin{Kind: "manual"}})
		if _, err = db.Exec("INSERT INTO tasks VALUES('owner',?,'complete',1,?)", kind, string(raw)); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec("INSERT INTO task_run_history VALUES('owner',?,'catalogue-run',?)", kind, string(raw)); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec("INSERT INTO task_writes VALUES('owner',?,?,?)", kind, hash, kind); err != nil {
			t.Fatal(err)
		}
	}
	// Retired local process destinations remain recovery evidence.
	for _, node := range []string{"local", "retired-runner"} {
		request := legacyCreateTask{MachineID: node, RequestID: node, Text: "retained input"}
		raw, _ := json.Marshal(map[string]any{"schema_version": 12, "request": request, "origin": protocol.TaskOrigin{Kind: "manual"}, "launch": map[string]any{"runtime_id": node, "control_id": "local", "run_id": "original-run"}})
		if _, err = db.Exec("INSERT INTO tasks VALUES('owner',?,'running',1,?)", node, string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if err = Migrate(context.Background(), db, TasksDatabase); err != nil {
			t.Fatal(err)
		}
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = OpenDatabase(path, TasksDatabase)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, kind := range []string{"node", "assistant", "unresolved"} {
		for _, table := range []string{"tasks", "task_run_history"} {
			column := "id"
			if table == "task_run_history" {
				column = "task_id"
			}
			var raw string
			if err = db.QueryRow("SELECT body FROM "+table+" WHERE "+column+"=?", kind).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			doc, err := targetDecode(raw)
			if err != nil {
				t.Fatal(err)
			}
			execution := targetField[targetDocument](doc, "execution")
			receipt := targetField[targetDocument](execution, "receipt")
			request := targetField[protocol.CreateTask](doc, "request")
			wire := targetField[targetTaskRequest](execution, "request")
			if targetField[int](doc, "schema_version") != 13 || !targetField[bool](execution, "notified") || !targetField[bool](execution, "stop_requested") || targetField[string](doc, "run_id") != "catalogue-run" {
				t.Fatal(raw)
			}
			if kind == "unresolved" {
				if targetField[string](receipt, "state") != "unknown" || targetField[string](receipt, "error") != "agent_link_target_unresolved" || len(execution["unresolved_request"]) == 0 || request.Target != nil {
					t.Fatal("reassigned an unresolved default", raw)
				}
			} else {
				expected := targetNode("remote")
				if kind == "assistant" {
					expected = targetAssistant("remote", "original-assistant")
				}
				if request.Target == nil || *request.Target != expected || wire.Target != expected || targetField[string](receipt, "id") != "receiver-task-"+kind || targetField[string](receipt, "run_id") != "run-"+kind || targetField[string](receipt, "result") != "saved result" || len(receipt["execution"]) != 0 || len(receipt["assistant"]) != 0 {
					t.Fatal(raw)
				}
			}
			if table == "tasks" {
				var digest string
				expected, _ := core.HashJSON([]any{request, protocol.TaskOrigin{Kind: "manual"}})
				if err = db.QueryRow("SELECT digest FROM task_writes WHERE request_id=?", kind).Scan(&digest); err != nil || digest != expected {
					t.Fatal(digest, expected, err)
				}
			}
		}
	}
	for _, node := range []string{"local", "retired-runner"} {
		var actual, run, removed string
		if err = db.QueryRow("SELECT json_extract(body,'$.node_id'),json_extract(body,'$.launch.runtime_id'),COALESCE(json_extract(body,'$.request.machine_id'),'') FROM tasks WHERE id=?", node).Scan(&actual, &run, &removed); err != nil || actual != node || run != node || removed != "" {
			t.Fatal(actual, run, removed, err)
		}
	}
	var events int
	if err = db.QueryRow("SELECT count(*) FROM agent_messages").Scan(&events); err != nil || events != 0 {
		t.Fatal("upgrade dispatched work", events, err)
	}
}

func TestTaskTargetControlUpgradeBindsDirectoryCapabilitiesAndOriginalReceiver(t *testing.T) {
	db, err := OpenSQLite(filepath.Join(t.TempDir(), "control.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	nodeMigrationPrefix(t, db, ControlDatabase, 30)
	identity := map[string]any{"address": targetAssistant("local", "original"), "name": "Original", "adapter": "fixture-agent"}
	caps := map[string]any{"version": 1, "scope": "peer", "operations": []string{"task.submit", "task.read"}}
	directory := map[string]any{"node_id": "local", "name": "Agent", "adapter": "fixture-agent", "assistants": []any{identity, map[string]any{"address": targetAssistant("local", "new-default"), "name": "New default", "adapter": "fixture-agent"}}, "default_assistant_id": "new-default", "operations": map[string]any{"original": caps, "new-default": caps}, "task_executor": map[string]any{"version": 1, "scope": "caller", "operations": []string{"task.submit"}}}
	metadata, _ := json.Marshal(map[string]any{"local": map[string]any{"address": targetNode("local")}, "peer_host": map[string]any{"node_id": "caller"}, "directory": directory})
	if _, err = db.Exec("INSERT INTO agent_link_connections VALUES('saved-link','route','ready',?,'saved-secret','saved-pin')", string(metadata)); err != nil {
		t.Fatal(err)
	}
	from := targetAssistant("caller", "owner")
	old := executorUpgradeRequest{RequestID: "accepted", From: from, Text: "已接受 <operation> & \"quote\""}
	raw, _ := json.Marshal(old)
	if _, err = db.Exec("INSERT INTO agent_link_received_tasks VALUES('receipt','caller','accepted','original',?,'original-task')", string(raw)); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = Migrate(context.Background(), db, ControlDatabase); err != nil {
			t.Fatal(err)
		}
	}
	var payload, nodeTarget, assistantTarget, secret, pin, task string
	if err = db.QueryRow("SELECT payload,task_id FROM agent_link_received_tasks").Scan(&payload, &task); err != nil {
		t.Fatal(err)
	}
	expected, _ := json.Marshal(targetTaskRequest{Target: targetAssistant("local", "original"), From: from, RequestID: old.RequestID, Text: old.Text})
	if payload != string(expected) || task != "original-task" {
		t.Fatal("retry binding changed", payload, string(expected), task)
	}
	if err = db.QueryRow("SELECT json_extract(metadata,'$.directory.targets[0].capabilities.scope'),json_extract(metadata,'$.directory.targets[2].address.kind'),secret_ref,peer_key FROM agent_link_connections").Scan(&assistantTarget, &nodeTarget, &secret, &pin); err != nil || assistantTarget != "peer" || nodeTarget != "node" || secret != "saved-secret" || pin != "saved-pin" {
		t.Fatal(assistantTarget, nodeTarget, secret, pin, err)
	}
}

func TestTaskTargetUpgradeFailureRollsBackBindingsAndCanRetry(t *testing.T) {
	db, err := OpenSQLite(filepath.Join(t.TempDir(), "tasks.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	nodeMigrationPrefix(t, db, TasksDatabase, 25)
	original := `{"schema_version":12,"request":{"machine_id":"local","request_id":"request","text":"work"},"origin":{"kind":"manual"}}`
	if _, err = db.Exec("INSERT INTO tasks VALUES('owner','task','queued',1,?)", original); err != nil {
		t.Fatal(err)
	}
	execMigrationSQL(t, db, `INSERT INTO task_writes VALUES('owner','request','original-digest','task'); INSERT INTO codex_link_jobs(id,caller,request_id,payload,data) VALUES('job','caller','request','{}','{"task":{"execution":{"address":{"node_id":"remote","kind":"task","agent_id":"job"}}},"request":{"executor":"agent","from":"corrupt"},"cleanup_uncertain":true}')`)
	if err = Migrate(context.Background(), db, TasksDatabase); err == nil {
		t.Fatal("corrupt request converted")
	}
	var body, digest string
	if err = db.QueryRow("SELECT body FROM tasks WHERE id='task'").Scan(&body); err != nil || body != original {
		t.Fatal("partially converted task", body, err)
	}
	if err = db.QueryRow("SELECT digest FROM task_writes").Scan(&digest); err != nil || digest != "original-digest" {
		t.Fatal("partially converted binding", digest, err)
	}
	var receipts int
	if err = db.QueryRow("SELECT count(*) FROM pot_migrations WHERE id LIKE '0026_%'").Scan(&receipts); err != nil || receipts != 0 {
		t.Fatal(receipts, err)
	}
	execMigrationSQL(t, db, `UPDATE codex_link_jobs SET data=json_set(data,'$.request.from',json('{"node_id":"local","kind":"assistant","agent_id":"owner"}'))`)
	for range 2 {
		if err = Migrate(context.Background(), db, TasksDatabase); err != nil {
			t.Fatal(err)
		}
	}
	if err = db.QueryRow("SELECT json_extract(body,'$.node_id') FROM tasks WHERE id='task'").Scan(&body); err != nil || body != "local" {
		t.Fatal(body, err)
	}
}

func TestUnresolvedArchivedSubmissionImportRetainsEvidenceWithoutResubmission(t *testing.T) {
	root := t.TempDir()
	source, err := OpenSQLite(filepath.Join(root, "control.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	target, err := OpenSQLite(filepath.Join(root, "tasks.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	nodeMigrationPrefix(t, source, ControlDatabase, 30)
	nodeMigrationPrefix(t, target, TasksDatabase, 25)
	request := legacyCreateTask{LinkID: "saved-link", RequestID: "pending", Text: "Original instructions"}
	wire := executorUpgradeRequest{RequestID: request.RequestID, From: targetAssistant("local", "owner"), Text: request.Text}
	body, _ := json.Marshal(map[string]any{"info": map[string]any{"id": "pending-task", "assistant_id": "owner", "phase": "queued", "agent_link": map[string]any{"link_id": "saved-link", "node_id": "remote", "kind": "pot"}}, "request": wire, "remote": map[string]any{}})
	payload, _ := json.Marshal(request)
	if _, err = source.Exec("INSERT INTO retired_task_submissions(id,link_id,owner,request_id,payload,body,stop_requested) VALUES('pending-task','saved-link','owner','pending',?,?,1)", string(payload), string(body)); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = ImportTaskSubmissions(root); err != nil {
			t.Fatal(err)
		}
	}
	var phase, state, evidence string
	var stop bool
	if err = target.QueryRow("SELECT json_extract(body,'$.info.phase'),json_extract(body,'$.execution.receipt.state'),json_extract(body,'$.execution.unresolved_request.text'),json_extract(body,'$.execution.stop_requested') FROM tasks WHERE id='pending-task'").Scan(&phase, &state, &evidence, &stop); err != nil || phase != "interrupted" || state != "unknown" || evidence != request.Text || !stop {
		t.Fatal(phase, state, evidence, stop, err)
	}
	var dispatched int
	if err = target.QueryRow("SELECT count(*) FROM agent_messages").Scan(&dispatched); err != nil || dispatched != 0 {
		t.Fatal(dispatched, err)
	}
}
