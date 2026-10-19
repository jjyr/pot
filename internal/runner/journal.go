package runner

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jjyr/pot/pkg/agent"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/storage"
	"time"
)

const SchemaVersion = 6

type Journal struct {
	db        *sql.DB
	RuntimeID string
	BootID    string
	changes   core.Changes
}

func Open(path, runtimeID, bootID string) (*Journal, error) {
	if !core.Identifier(runtimeID) || !core.Identifier(bootID) {
		return nil, fmt.Errorf("invalid Runner authority")
	}
	db, err := storage.OpenSQLite(path)
	if err != nil {
		return nil, err
	}
	j := &Journal{db: db, RuntimeID: runtimeID, BootID: bootID}
	if _, err = db.Exec(`CREATE TABLE IF NOT EXISTS runner_schema(version INTEGER NOT NULL); INSERT INTO runner_schema SELECT 6 WHERE NOT EXISTS(SELECT 1 FROM runner_schema); CREATE TABLE IF NOT EXISTS run_workspaces(control_id TEXT NOT NULL,task_id TEXT NOT NULL,run_id TEXT NOT NULL,body TEXT NOT NULL,PRIMARY KEY(control_id,task_id,run_id)); CREATE TABLE IF NOT EXISTS task_runs(control_id TEXT NOT NULL,task_id TEXT NOT NULL,run_id TEXT NOT NULL,digest TEXT NOT NULL,phase TEXT NOT NULL,boot_id TEXT NOT NULL,body TEXT NOT NULL,PRIMARY KEY(control_id,task_id,run_id)); CREATE TABLE IF NOT EXISTS task_events(control_id TEXT NOT NULL,task_id TEXT NOT NULL,run_id TEXT NOT NULL,sequence INTEGER NOT NULL,event TEXT NOT NULL,PRIMARY KEY(control_id,task_id,run_id,sequence)); CREATE TABLE IF NOT EXISTS run_command_inputs(control_id TEXT NOT NULL,task_id TEXT NOT NULL,run_id TEXT NOT NULL,command_id TEXT NOT NULL,body TEXT NOT NULL,PRIMARY KEY(control_id,task_id,run_id,command_id)); CREATE TABLE IF NOT EXISTS run_resources(control_id TEXT NOT NULL,task_id TEXT NOT NULL,run_id TEXT NOT NULL,digest TEXT NOT NULL,body TEXT NOT NULL,PRIMARY KEY(control_id,task_id,run_id)); CREATE TABLE IF NOT EXISTS run_commands(control_id TEXT NOT NULL,task_id TEXT NOT NULL,run_id TEXT NOT NULL,command_id TEXT NOT NULL,digest TEXT NOT NULL,phase TEXT NOT NULL,reply TEXT,PRIMARY KEY(control_id,task_id,run_id,command_id));`); err != nil {
		db.Close()
		return nil, err
	}
	var version int
	if err = db.QueryRow("SELECT version FROM runner_schema").Scan(&version); err != nil || version != SchemaVersion {
		db.Close()
		return nil, fmt.Errorf("unsupported Runner journal schema")
	}
	err = storage.Transaction(context.Background(), db, func(tx *sql.Tx) error {
		rows, err := tx.Query("SELECT control_id,task_id,run_id,body FROM task_runs")
		if err != nil {
			return err
		}
		runs := []Run{}
		for rows.Next() {
			var raw string
			var control, task, runID string
			if err := rows.Scan(&control, &task, &runID, &raw); err != nil {
				rows.Close()
				return err
			}
			var run Run
			if err := core.DecodeJSON([]byte(raw), &run); err != nil {
				rows.Close()
				return err
			}
			if err := j.validateRun(run); err != nil {
				rows.Close()
				return err
			}
			if run.Launch.ControlID != control || run.Launch.TaskID != task || run.Launch.RunID != runID {
				rows.Close()
				return fmt.Errorf("Runner journal key identity mismatch")
			}
			runs = append(runs, run)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for _, run := range runs {
			if !run.Phase.Ended() && run.BootID != bootID {
				run.Phase = RecoveryRequired
				if err := writeRun(context.Background(), tx, run); err != nil {
					return err
				}
				if _, err := tx.Exec("UPDATE run_commands SET phase='unknown' WHERE control_id=? AND task_id=? AND run_id=? AND phase='pending'", run.Launch.ControlID, run.Launch.TaskID, run.Launch.RunID); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		db.Close()
		return nil, err
	}
	return j, nil
}
func (j *Journal) Close() error { return j.db.Close() }

// CheckPolicyCleanup prevents a fresh Runner boot from acknowledging revocation
// while prior execution still requires authoritative cleanup reconciliation.
func (j *Journal) CheckPolicyCleanup(ctx context.Context) error {
	var unknown bool
	if err := j.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM task_runs WHERE phase=?) OR EXISTS(SELECT 1 FROM run_commands WHERE phase='unknown')", RecoveryRequired).Scan(&unknown); err != nil {
		return err
	}
	if unknown {
		return core.ErrCleanupUnconfirmed
	}
	return nil
}
func (j *Journal) Subscribe() *core.Subscription { return j.changes.Subscribe() }
func (j *Journal) validateRun(run Run) error {
	if run.SchemaVersion != SchemaVersion || !core.Identifier(run.BootID) {
		return fmt.Errorf("invalid run journal identity/schema")
	}
	if err := run.Launch.Validate(j.RuntimeID); err != nil {
		return err
	}
	hash, err := run.Launch.Digest()
	if err != nil {
		return err
	}
	if hash != run.LaunchDigest || run.Phase.Ended() != (run.Result != nil) {
		return fmt.Errorf("inconsistent run checksum/terminal state")
	}
	if run.Result != nil {
		phase, err := run.Result.phase()
		if err != nil || phase != run.Phase && !(run.Phase == Interrupted && run.Result.Outcome.Kind == "cancelled") {
			return fmt.Errorf("run terminal outcome conflict")
		}
	}
	switch run.Phase {
	case Accepted, Running, Waiting, Complete, Failed, Cancelled, RecoveryRequired, Interrupted:
	default:
		return fmt.Errorf("unknown run phase")
	}
	return nil
}

type querier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readRun(ctx context.Context, q querier, control, task, run string) (*Run, error) {
	var raw string
	err := q.QueryRowContext(ctx, "SELECT body FROM task_runs WHERE control_id=? AND task_id=? AND run_id=?", control, task, run).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var value Run
	if err := core.DecodeJSON([]byte(raw), &value); err != nil {
		return nil, err
	}
	if value.Launch.ControlID != control || value.Launch.TaskID != task || value.Launch.RunID != run {
		return nil, fmt.Errorf("run journal key identity mismatch")
	}
	return &value, nil
}
func writeRun(ctx context.Context, tx *sql.Tx, run Run) error {
	raw, err := json.Marshal(run)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO task_runs(control_id,task_id,run_id,digest,phase,boot_id,body) VALUES(?,?,?,?,?,?,?) ON CONFLICT(control_id,task_id,run_id) DO UPDATE SET phase=excluded.phase,boot_id=excluded.boot_id,body=excluded.body", run.Launch.ControlID, run.Launch.TaskID, run.Launch.RunID, run.LaunchDigest, run.Phase, run.BootID, string(raw))
	return err
}
func (j *Journal) Inspect(ctx context.Context, control, task, run string) (*Run, error) {
	return readRun(ctx, j.db, control, task, run)
}
func (j *Journal) Accept(ctx context.Context, launch Launch) (value Run, err error) {
	if err = launch.Validate(j.RuntimeID); err != nil {
		return value, err
	}
	hash, err := launch.Digest()
	if err != nil {
		return value, err
	}
	err = storage.Transaction(ctx, j.db, func(tx *sql.Tx) error {
		old, err := readRun(ctx, tx, launch.ControlID, launch.TaskID, launch.RunID)
		if err != nil {
			return err
		}
		if old != nil {
			if old.LaunchDigest != hash {
				return fmt.Errorf("launch conflicts with accepted immutable input")
			}
			value = *old
			return nil
		}
		rows, err := tx.QueryContext(ctx, "SELECT body FROM task_runs WHERE control_id=? AND task_id=?", launch.ControlID, launch.TaskID)
		if err != nil {
			return err
		}
		sequence := uint64(1)
		var previous *Run
		for rows.Next() {
			var raw string
			if err := rows.Scan(&raw); err != nil {
				rows.Close()
				return err
			}
			var r Run
			if err := core.DecodeJSON([]byte(raw), &r); err != nil {
				rows.Close()
				return err
			}
			if !r.Phase.Ended() {
				rows.Close()
				return fmt.Errorf("previous task run requires reconciliation")
			}
			if previous == nil || r.Sequence > previous.Sequence {
				copy := r
				previous = &copy
			}
		}
		if err := rows.Close(); err != nil {
			return err
		}
		var checkpoint json.RawMessage
		if previous != nil {
			if previous.Launch.Workspace != launch.Workspace {
				return fmt.Errorf("Task workspace selection changed")
			}
			if previous.Sequence >= core.MaxSafeInteger {
				return fmt.Errorf("task sequence exhausted")
			}
			sequence = previous.Sequence + 1
			if previous.Launch.Generation == launch.Generation && previous.Launch.ExecutionTarget == launch.ExecutionTarget && previous.Launch.Config.SpecHash == launch.Config.SpecHash {
				checkpoint = previous.Context
			}
		}
		value = Run{SchemaVersion: SchemaVersion, Launch: launch, LaunchDigest: hash, BootID: j.BootID, Phase: Accepted, Context: checkpoint, Sequence: sequence}
		return writeRun(ctx, tx, value)
	})
	return value, err
}
func (j *Journal) change(ctx context.Context, control, task, run string, fn func(*Run) error) error {
	return j.changeTx(ctx, control, task, run, func(_ *sql.Tx, value *Run) error { return fn(value) })
}
func (j *Journal) changeTx(ctx context.Context, control, task, run string, fn func(*sql.Tx, *Run) error) error {
	changed := false
	err := storage.Transaction(ctx, j.db, func(tx *sql.Tx) error {
		value, err := readRun(ctx, tx, control, task, run)
		if err != nil {
			return err
		}
		if value == nil {
			return fmt.Errorf("task run not accepted")
		}
		before, _ := json.Marshal(value)
		if err := fn(tx, value); err != nil {
			return err
		}
		after, _ := json.Marshal(value)
		if string(before) == string(after) {
			return nil
		}
		changed = true
		return writeRun(ctx, tx, *value)
	})
	if err == nil && changed {
		j.changes.Notify("runner")
	}
	return err
}
func (j *Journal) Claim(ctx context.Context, control, task, run string) (claimed bool, err error) {
	err = j.change(ctx, control, task, run, func(value *Run) error {
		if value.Phase != Accepted {
			return nil
		}
		if value.BootID != j.BootID {
			return fmt.Errorf("run belongs to previous boot")
		}
		value.Phase = Running
		if value.StartedAtMS == 0 {
			value.StartedAtMS = time.Now().UnixMilli()
		}
		claimed = true
		return nil
	})
	return claimed, err
}
func (j *Journal) SaveContext(ctx context.Context, control, task, run string, raw json.RawMessage) error {
	if err := storage.CheckContext(raw); err != nil {
		return err
	}
	return j.change(ctx, control, task, run, func(value *Run) error {
		if value.Phase != Running || value.BootID != j.BootID {
			return fmt.Errorf("run is fenced")
		}
		value.Context = append(json.RawMessage(nil), raw...)
		return nil
	})
}
func (j *Journal) Finish(ctx context.Context, control, task, run string, result Terminal) error {
	defer j.changes.Notify("runner")
	phase, err := result.phase()
	if err != nil {
		return err
	}
	return storage.Transaction(ctx, j.db, func(tx *sql.Tx) error {
		value, err := readRun(ctx, tx, control, task, run)
		if err != nil {
			return err
		}
		if value == nil {
			return fmt.Errorf("task run not accepted")
		}
		if value.Result != nil {
			if *value.Result != result {
				return fmt.Errorf("conflicting terminal receipt")
			}
			return nil
		}
		if value.Phase != Running || value.BootID != j.BootID {
			return fmt.Errorf("run is not current")
		}
		var pending int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM run_commands WHERE control_id=? AND task_id=? AND run_id=? AND phase!='complete'", control, task, run).Scan(&pending); err != nil {
			return err
		}
		if pending > 0 {
			return core.ErrCleanupUnconfirmed
		}
		value.Phase = phase
		value.Result = &result
		value.CleanupConfirmed = true
		return writeRun(ctx, tx, *value)
	})
}
func (j *Journal) Wait(ctx context.Context, control, task, run, detail string) error {
	return j.changeTx(ctx, control, task, run, func(tx *sql.Tx, value *Run) error {
		if value.Phase != Running || value.BootID != j.BootID {
			return fmt.Errorf("run is fenced")
		}
		if len(value.Context) > 0 {
			var state agent.Checkpoint
			if err := core.DecodeJSON(value.Context, &state); err != nil {
				return err
			}
			if state.Started != nil || state.ModelRequestID != nil {
				return fmt.Errorf("unsettled run cannot wait for allowance")
			}
		}
		var pending int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM run_commands WHERE control_id=? AND task_id=? AND run_id=? AND phase!='complete'", control, task, run).Scan(&pending); err != nil {
			return err
		}
		if pending > 0 {
			return core.ErrCleanupUnconfirmed
		}
		value.Phase = Waiting
		value.WaitDetail = detail
		value.CleanupConfirmed = true
		return nil
	})
}
func (j *Journal) Resume(ctx context.Context, control, task, run string) error {
	return j.change(ctx, control, task, run, func(value *Run) error {
		if value.Phase != Waiting || value.BootID != j.BootID || !value.CleanupConfirmed || value.StopRequested != "" {
			return fmt.Errorf("run cannot resume")
		}
		value.Phase = Accepted
		value.Finalizing = false
		value.CleanupConfirmed = false
		value.WaitDetail = ""
		return nil
	})
}
func (j *Journal) Fence(ctx context.Context, control, task, run string) error {
	return j.change(ctx, control, task, run, func(value *Run) error {
		if value.Phase.Ended() {
			return fmt.Errorf("terminal run cannot be fenced")
		}
		value.Phase = RecoveryRequired
		return nil
	})
}
func (j *Journal) ReconcileRecovered(ctx context.Context, control, task, run string, commandReplies, receipts map[string]json.RawMessage, cleanupConfirmed bool) error {
	if !cleanupConfirmed {
		return core.ErrCleanupUnconfirmed
	}
	return j.changeTx(ctx, control, task, run, func(tx *sql.Tx, value *Run) error {
		if value.Phase != RecoveryRequired {
			return fmt.Errorf("run does not require recovery")
		}
		for id, reply := range commandReplies {
			command, err := readCommand(ctx, tx, control, task, run, id)
			if err != nil {
				return err
			}
			if command == nil {
				continue
			}
			if !json.Valid(reply) || len(reply) > 16*1024*1024 {
				return fmt.Errorf("invalid reconciled command reply")
			}
			if command.Phase == "complete" {
				a, err := core.HashJSON(command.Reply)
				if err != nil {
					return err
				}
				b, err := core.HashJSON(reply)
				if err != nil {
					return err
				}
				if a != b {
					return fmt.Errorf("reconciled command differs from original receipt")
				}
			} else {
				if _, err := tx.ExecContext(ctx, "UPDATE run_commands SET phase='complete',reply=? WHERE control_id=? AND task_id=? AND run_id=? AND command_id=?", string(reply), control, task, run, id); err != nil {
					return err
				}
			}
		}
		var pending int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM run_commands WHERE control_id=? AND task_id=? AND run_id=? AND phase!='complete'", control, task, run).Scan(&pending); err != nil {
			return err
		}
		if pending > 0 {
			return core.ErrCleanupUnconfirmed
		}
		if len(value.Context) > 0 {
			vision := false
			if catalog := value.Launch.Config.Params.Agent.ModelCatalog; catalog != nil {
				descriptor, err := catalog.Resolve(value.Launch.Config.Provider, value.Launch.Config.Model)
				if err != nil {
					return err
				}
				vision = descriptor.Vision()
			}
			raw, err := agent.ReconcileContext(value.Context, receipts, vision)
			if err != nil {
				return err
			}
			var checkpoint agent.Checkpoint
			if err := core.DecodeJSON(raw, &checkpoint); err != nil {
				return err
			}
			if checkpoint.Started != nil {
				return core.ErrCleanupUnconfirmed
			}
			value.Context = raw
		}
		value.Phase = Interrupted
		value.Result = &Terminal{Outcome: core.Cancelled, Text: "Execution stopped after receipt reconciliation. Create a new task to continue."}
		value.CleanupConfirmed = true
		return nil
	})
}
