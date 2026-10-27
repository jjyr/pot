package agenthost

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

func (b *backend) ownerExecutor(ctx context.Context, workspace, mode string, settings ...string) error {
	model, effort := "", ""
	if len(settings) == 2 {
		model, effort = settings[0], settings[1]
	}
	if b.runtime().Kind == "antigravity" {
		if err := b.validateModel(ctx, model, effort); err != nil {
			return err
		}
	}
	if !filepath.IsAbs(workspace) || (mode != "read-only" && mode != "workspace-write") {
		return agentlink.ErrInvalid
	}
	path, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return agentlink.ErrInvalid
	}
	_, err = b.db.ExecContext(ctx, `INSERT INTO agent_adapter_executor(singleton,workspace,mode,model,effort) VALUES(1,?,?,?,?) ON CONFLICT(singleton) DO UPDATE SET workspace=excluded.workspace,mode=excluded.mode,model=excluded.model,effort=excluded.effort`, path, mode, model, effort)
	return err
}

// submitTask never resolves an assistant. Each accepted task snapshots the
// receiver-owned execution scope and owns its own persistent Codex thread.
func (b *backend) submitTask(ctx context.Context, r agentlink.TaskRequest) (agentlink.Task, error) {
	if r.Target != agentlink.NodeTarget(b.nodeID) || r.From.Kind != "assistant" || r.Workspace != "" {
		return agentlink.Task{}, agentlink.ErrInvalid
	}
	var accepted job
	err := storage.Transaction(ctx, b.db, func(tx *sql.Tx) error {
		return b.submitTaskTx(ctx, tx, r, &accepted)
	})
	return accepted.Task, err
}

// submitTaskTx lets receiver admission commit the real execution mapping with
// the existing job and session receipt, before the scheduler can launch it.
func (b *backend) submitTaskTx(ctx context.Context, tx *sql.Tx, r agentlink.TaskRequest, accepted *job) error {
	var raw, payload string
	err := tx.QueryRowContext(ctx, "SELECT payload,data FROM agent_adapter_jobs WHERE caller=? AND request_id=?", callerKey(r.From), r.RequestID).Scan(&payload, &raw)
	if err == nil {
		if payload != encoded(r) {
			return agentlink.ErrConflict
		}
		return json.Unmarshal([]byte(raw), accepted)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var count, total int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(json_extract(data,'$.task.state') NOT IN ('completed','failed','cancelled')),0) FROM agent_adapter_jobs`).Scan(&total, &count); err != nil {
		return err
	}
	if count >= 32 || total >= 256 {
		return agentlink.ErrCapacity
	}
	var workspace, mode, model, effort string
	if err := tx.QueryRowContext(ctx, "SELECT workspace,mode,model,effort FROM agent_adapter_executor WHERE singleton=1").Scan(&workspace, &mode, &model, &effort); err != nil {
		return err
	}
	now := time.Now().UTC()
	id := jobID(r.From, r.RequestID)
	identity := agentlink.Identity{Address: protocol.AgentRef{NodeID: b.nodeID, Kind: "task", AgentID: id}, Name: b.runtime().Name, Adapter: b.runtime().Kind}
	*accepted = job{Task: agentlink.Task{ID: id, RunID: id, RequestID: r.RequestID, Receiver: identity, State: "queued", CreatedAt: now, UpdatedAt: now}, Request: r}
	if _, err := tx.ExecContext(ctx, "INSERT INTO agent_adapter_jobs(id,caller,request_id,assistant_id,payload,data) VALUES(?,?,?,NULL,?,?)", id, callerKey(r.From), r.RequestID, encoded(r), encoded(accepted)); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO agent_adapter_sessions(task_id,workspace,mode,model,effort) VALUES(?,?,?,?,?)", id, workspace, mode, model, effort)
	return err
}

func (b *backend) Input(ctx context.Context, r agentlink.TaskGuidance) (agentlink.Task, error) {
	if e := b.requireCurrentWork(ctx, r.ID); e != nil {
		return agentlink.Task{}, e
	}
	if !validID(r.RequestID) || r.Text == "" || len(r.Text) > 24000 {
		return agentlink.Task{}, agentlink.ErrInvalid
	}
	var result job
	err := storage.Transaction(ctx, b.db, func(tx *sql.Tx) error {
		return b.inputTx(ctx, tx, r, "", &result)
	})
	return result.Task, err
}

// A receiver continuation checks the old run in the same transaction as the
// native continuation receipt and its new execution mapping.
func (b *backend) inputTx(ctx context.Context, tx *sql.Tx, r agentlink.TaskGuidance, expectedRun string, result *job) error {
	var raw string
	if err := tx.QueryRowContext(ctx, "SELECT data FROM agent_adapter_jobs WHERE id=? AND caller=?", r.ID, callerKey(r.From)).Scan(&raw); err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(raw), result); err != nil {
		return err
	}
	if result.Request.Target.Kind != "node" || result.Message != nil {
		return agentlink.ErrUnsupported
	}
	var prior string
	err := tx.QueryRowContext(ctx, "SELECT text FROM agent_adapter_inputs WHERE task_id=? AND request_id=?", r.ID, r.RequestID).Scan(&prior)
	if err == nil {
		if prior != r.Text {
			return agentlink.ErrConflict
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if expectedRun != "" && result.Task.RunID != expectedRun {
		return agentlink.ErrFenced
	}
	if !terminal(result.Task.State) {
		return agentlink.ErrConflict
	}
	if result.CleanupUncertain {
		return agentlink.ErrFenced
	}
	var active int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM agent_adapter_jobs WHERE json_extract(data,'$.task.state') NOT IN ('completed','failed','cancelled')`).Scan(&active); err != nil {
		return err
	}
	if active >= 32 {
		return agentlink.ErrCapacity
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO agent_adapter_inputs(task_id,request_id,text,previous_run) VALUES(?,?,?,?)", r.ID, r.RequestID, r.Text, raw); err != nil {
		return err
	}
	result.Task.State, result.Task.Result, result.Task.Error, result.TurnID = "queued", "", "", ""
	result.Task.RunID = jobID(r.From, r.ID+":"+r.RequestID)
	result.Task.UpdatedAt = time.Now().UTC()
	_, err = tx.ExecContext(ctx, "UPDATE agent_adapter_jobs SET data=? WHERE id=?", encoded(result), r.ID)
	return err
}

func (b *backend) runTask(ctx context.Context, selected job) {
	var workspace, mode, thread, model, effort string
	err := b.db.QueryRowContext(ctx, "SELECT workspace,mode,thread_id,model,effort FROM agent_adapter_sessions WHERE task_id=?", selected.Task.ID).Scan(&workspace, &mode, &thread, &model, &effort)
	if err != nil {
		_ = b.update(ctx, selected.Task.ID, func(j *job) error {
			j.Task.State, j.Task.Error = "failed", "codex_task_session_unavailable"
			return nil
		})
		return
	}
	text := selected.Request.Text
	var guidance string
	if err := b.db.QueryRowContext(ctx, "SELECT text FROM agent_adapter_inputs WHERE task_id=? ORDER BY rowid DESC LIMIT 1", selected.Task.ID).Scan(&guidance); err == nil {
		text = guidance
	}
	b.executeSession(ctx, selected, Session{JobID: selected.Task.ID, Binary: b.binary, Workspace: workspace, Mode: mode, ThreadID: thread, Worker: b.worker, Model: model, Effort: effort}, text, func(thread string) error {
		res, err := b.db.ExecContext(context.Background(), "UPDATE agent_adapter_sessions SET thread_id=? WHERE task_id=? AND (thread_id='' OR thread_id=?)", thread, selected.Task.ID, thread)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err == nil && n != 1 {
			return agentlink.ErrConflict
		}
		return err
	})
}
