package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

// ImportTaskSubmissions consolidates caller-side Agent Link tasks with local
// tasks. The import receipt commits with all rows; it never submits executions.
func ImportTaskSubmissions(root string) error {
	target, err := OpenDatabase(filepath.Join(root, "tasks.sqlite"), TasksDatabase)
	if err != nil {
		return err
	}
	defer target.Close()
	var pending int
	if err = target.QueryRow("SELECT count(*) FROM task_submission_import").Scan(&pending); err != nil || pending == 0 {
		return err
	}
	source, err := OpenDatabase(filepath.Join(root, "control.sqlite"), ControlDatabase)
	if err != nil {
		return err
	}
	defer source.Close()
	return Transaction(context.Background(), target, func(tx *sql.Tx) error {
		rows, err := source.Query("SELECT id,owner,payload,body,notified,stop_requested FROM retired_task_submissions ORDER BY id")
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id, owner, payload, body string
			var notified, stop int
			if err = rows.Scan(&id, &owner, &payload, &body, &notified, &stop); err != nil {
				return err
			}
			var old struct {
				Info              protocol.TaskInfo `json:"info"`
				Request           json.RawMessage   `json:"request"`
				Remote            json.RawMessage   `json:"remote"`
				Archived          bool              `json:"archived"`
				UnresolvedRequest json.RawMessage   `json:"unresolved_request,omitempty"`
			}
			if err = json.Unmarshal([]byte(body), &old); err != nil {
				return err
			}
			if old.Info.ID != id || old.Info.AssistantID != owner || old.Info.AgentLink == nil {
				return fmt.Errorf("invalid original task submission")
			}
			var request protocol.CreateTask
			if err = json.Unmarshal([]byte(payload), &request); err != nil {
				return err
			}
			origin := protocol.TaskOrigin{Kind: "manual"}
			record := map[string]any{"schema_version": 14, "plugins": []string{}, "assistant_id": owner, "info": old.Info, "request": request, "run_id": old.Info.RunID, "generation": 1, "revision": 1, "origin": origin, "execution": map[string]any{"request": old.Request, "receipt": old.Remote, "notified": notified != 0, "stop_requested": stop != 0}}
			if len(old.UnresolvedRequest) > 0 {
				record["execution"].(map[string]any)["unresolved_request"] = old.UnresolvedRequest
			}
			var receipt struct {
				Result string `json:"result"`
				RunID  string `json:"run_id"`
				Error  string `json:"error"`
			}
			if err = json.Unmarshal(old.Remote, &receipt); err != nil {
				return err
			}
			if old.Info.RunID == "" && old.Info.Phase.Scope() == "attention" {
				ref := receipt.RunID
				if ref == "" {
					ref = id
				}
				hash, _ := core.HashJSON([]string{"agent-link-chat", "task", ref})
				old.Info.RunID = hash[:8] + "-" + hash[8:12] + "-" + hash[12:16] + "-" + hash[16:20] + "-" + hash[20:32]
				record["info"] = old.Info
				record["run_id"] = old.Info.RunID
			}
			record["error"] = receipt.Error
			if old.Info.Phase.Scope() == "attention" {
				kind := "success"
				if old.Info.Phase == protocol.TaskFailed {
					kind = "failed"
				}
				if old.Info.Phase == protocol.TaskCancelled || old.Info.Phase == protocol.TaskInterrupted {
					kind = "cancelled"
				}
				record["result"] = map[string]any{"text": receipt.Result, "outcome": core.Outcome{Kind: kind, Error: receipt.Error}}
				record["outcome"] = protocol.TaskResultEvent{TaskID: id, RunID: old.Info.RunID, Title: old.Info.Title, Phase: old.Info.Phase, Text: receipt.Result, Error: receipt.Error, Origin: origin}
			}
			raw, err := json.Marshal(record)
			if err != nil {
				return err
			}
			if _, err = tx.Exec("INSERT INTO tasks(assistant_id,id,phase,created,body)VALUES(?,?,?,?,?)", owner, id, old.Info.Phase, old.Info.CreatedAtMS, string(raw)); err != nil {
				return err
			}
			// Retain the existing outcome cursor when delivery committed before
			// the old notified flag. A pending new run must still publish its outcome.
			var delivered bool
			if err = tx.QueryRow("SELECT EXISTS(SELECT 1 FROM agent_messages WHERE json_extract(envelope,'$.result.task_id')=? AND json_extract(envelope,'$.result.run_id')=?)", id, old.Info.RunID).Scan(&delivered); err != nil {
				return err
			}
			var caller struct {
				From protocol.AgentRef `json:"from"`
			}
			if err = json.Unmarshal(old.Request, &caller); err != nil {
				return err
			}
			if notified != 0 || delivered || caller.From.Kind == "user" {
				if _, err = tx.Exec("UPDATE task_outcomes SET run_id=? WHERE sequence=(SELECT MAX(sequence) FROM task_outcomes WHERE assistant_id=? AND task_id=? AND phase=?)", old.Info.RunID, owner, id, old.Info.Phase); err != nil {
					return err
				}
			}
			hash, err := core.HashJSON([]any{request, origin})
			if err != nil {
				return err
			}
			if _, err = tx.Exec("INSERT INTO task_writes(assistant_id,request_id,digest,task_id)VALUES(?,?,?,?)", owner, request.RequestID, hash, id); err != nil {
				return err
			}
		}
		if err = rows.Err(); err != nil {
			return err
		}
		_, err = tx.Exec("DELETE FROM task_submission_import")
		return err
	})
}
