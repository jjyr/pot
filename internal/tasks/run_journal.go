package tasks

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jjyr/pot/pkg/agent"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
	"time"
)

const SchemaVersion = 7

type Journal struct {
	db        *sql.DB
	NodeID    string
	BootID    string
	changes   core.Changes
	resources storage.ResourceFiles
}

// OpenJournal borrows an already migrated Tasks database. The caller owns Close.
func OpenJournal(db *sql.DB, cache, nodeID, bootID string) (*Journal, error) {
	if !core.Identifier(nodeID) || !core.Identifier(bootID) {
		return nil, fmt.Errorf("invalid local task node identity")
	}
	if db == nil {
		return nil, fmt.Errorf("run journal requires a Tasks database")
	}

	j := &Journal{db: db, NodeID: nodeID, BootID: bootID, resources: storage.ResourceFiles{Root: cache}}
	err := storage.Transaction(context.Background(), db, func(tx *sql.Tx) error {
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
				return fmt.Errorf("task journal key identity mismatch")
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
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return j, nil
}

// CheckPolicyCleanup prevents a fresh node boot from acknowledging revocation
// while prior execution still requires authoritative cleanup reconciliation.
func (j *Journal) CheckPolicyCleanup(ctx context.Context) error {
	var unknown bool
	if err := j.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM task_runs WHERE phase=?)", RecoveryRequired).Scan(&unknown); err != nil {
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
	if err := run.Launch.Validate(j.NodeID); err != nil {
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
	err = storage.Transaction(ctx, j.db, func(tx *sql.Tx) error { var e error; value, e = j.acceptTx(ctx, tx, launch); return e })
	return
}
func (j *Journal) acceptTx(ctx context.Context, tx *sql.Tx, launch Launch) (value Run, err error) {
	if err = launch.Validate(j.NodeID); err != nil {
		return value, err
	}
	hash, err := launch.Digest()
	if err != nil {
		return value, err
	}
	err = (func(tx *sql.Tx) error {
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
		if launch.ControlID != j.NodeID {
			return protocol.APIError{Code: "remote_execution_removed"}
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
			if !r.Phase.Ended() || !r.CleanupConfirmed {
				rows.Close()
				return fmt.Errorf("previous task run requires reconciliation")
			}
			if previous == nil || r.Sequence > previous.Sequence {
				copy := r
				previous = &copy
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		var checkpoint json.RawMessage
		var eventSequence uint64
		if previous != nil {
			if previous.Launch.Workspace != launch.Workspace {
				return fmt.Errorf("Task workspace selection changed")
			}
			if previous.Sequence >= core.MaxSafeInteger {
				return fmt.Errorf("task sequence exhausted")
			}
			sequence = previous.Sequence + 1
			eventSequence = previous.EventSequence
			if previous.Launch.Generation == launch.Generation && previous.Launch.ExecutionTarget == launch.ExecutionTarget && previous.Launch.Config.SpecHash == launch.Config.SpecHash {
				checkpoint = previous.Context
			}
		}
		value = Run{SchemaVersion: SchemaVersion, Launch: launch, LaunchDigest: hash, BootID: j.BootID, Phase: Accepted, Context: checkpoint, Sequence: sequence, EventSequence: eventSequence}
		return writeRun(ctx, tx, value)
	})(tx)
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
		j.changes.Notify("runs")
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
	defer j.changes.Notify("runs")
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
func (j *Journal) ReconcileRecovered(ctx context.Context, control, task, run string, receipts map[string]json.RawMessage, cleanupConfirmed bool) error {
	if !cleanupConfirmed {
		return core.ErrCleanupUnconfirmed
	}
	return j.changeTx(ctx, control, task, run, func(tx *sql.Tx, value *Run) error {
		if value.Phase != RecoveryRequired {
			return fmt.Errorf("run does not require recovery")
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

func (j *Journal) Settle(ctx context.Context, control, task, run string) error {
	return j.changeTx(ctx, control, task, run, func(tx *sql.Tx, record *Run) error {
		if record.Phase != Running || record.BootID != j.BootID {
			return core.ErrCleanupUnconfirmed
		}

		record.Finalizing = true
		return nil
	})
}
