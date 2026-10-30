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
	"github.com/jjyr/pot/pkg/rollout"
	"github.com/jjyr/pot/pkg/storage"
	"sync"
	"time"
)

const SchemaVersion = 10

type Journal struct {
	db               *sql.DB
	rolloutDirectory string
	NodeID           string
	BootID           string
	changes          core.Changes
	resources        storage.ResourceFiles
	finishedMu       sync.RWMutex
	onFinished       func()
}

// OpenJournal borrows an already migrated Tasks database. The caller owns Close.
func OpenJournal(db *sql.DB, cache, nodeID, bootID string) (*Journal, error) {
	if !core.Identifier(nodeID) || !core.Identifier(bootID) {
		return nil, fmt.Errorf("invalid local task node identity")
	}
	if db == nil {
		return nil, fmt.Errorf("run journal requires a Tasks database")
	}

	directory, err := taskRolloutDirectory(db)
	if err != nil {
		return nil, err
	}
	j := &Journal{db: db, rolloutDirectory: directory, NodeID: nodeID, BootID: bootID, resources: storage.ResourceFiles{Root: cache}}
	err = storage.Transaction(context.Background(), db, func(tx *sql.Tx) error {
		rows, err := tx.Query("SELECT control_id,task_id,run_id FROM task_runs ORDER BY control_id,task_id,CAST(json_extract(body,'$.sequence') AS INTEGER)")
		if err != nil {
			return err
		}
		var keys [][3]string
		for rows.Next() {
			var key [3]string
			if err := rows.Scan(&key[0], &key[1], &key[2]); err != nil {
				rows.Close()
				return err
			}
			keys = append(keys, key)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		validatedHeads := map[[2]string]bool{}
		for _, key := range keys {
			run, err := readRun(context.Background(), tx, key[0], key[1], key[2])
			if err != nil {
				return err
			}
			if run == nil {
				return fmt.Errorf("task run disappeared during startup")
			}
			if err := j.validateRun(*run); err != nil {
				return err
			}
			if run.RolloutRef != nil {
				head, err := readTaskHead(context.Background(), tx, run.Launch.ControlID, run.Launch.TaskID)
				if err != nil {
					return err
				}
				if head.IsZero() {
					return fmt.Errorf("unsupported prototype per-run rollout; task session head missing")
				}
				if head.Sequence < run.RolloutRef.Sequence || head.Sequence == run.RolloutRef.Sequence && head.Hash != run.RolloutRef.Hash {
					return fmt.Errorf("task session head precedes historical checkpoint")
				}
				identity := [2]string{run.Launch.ControlID, run.Launch.TaskID}
				if !validatedHeads[identity] {
					if _, err := rollout.Load(j.rolloutPath(*run), head); err != nil {
						return fmt.Errorf("load task session head: %w", err)
					}
					validatedHeads[identity] = true
				}
				if err := j.hydrateRun(run); err != nil {
					return err
				}
			}
			if len(run.Context) > 0 && run.RolloutRef == nil {
				if err := j.persistRunCheckpoint(context.Background(), tx, run, true); err != nil {
					return err
				}
			}
			if err := ensureTaskSession(context.Background(), tx, *run); err != nil {
				return err
			}
			if !run.Phase.Ended() && run.BootID != bootID {
				run.Phase = RecoveryRequired
				if err := writeRun(context.Background(), tx, *run); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := j.reclaimMigratedRollouts(context.Background()); err != nil {
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

// SetOnFinished installs a lightweight wake-up callback. It runs outside the
// database transaction, once per newly committed terminal transition.
func (j *Journal) SetOnFinished(fn func()) {
	j.finishedMu.Lock()
	j.onFinished = fn
	j.finishedMu.Unlock()
}
func (j *Journal) notifyFinished() {
	j.finishedMu.RLock()
	fn := j.onFinished
	j.finishedMu.RUnlock()
	if fn != nil {
		fn()
	}
}
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
	if run.RolloutRef != nil {
		run.Context = nil
	}
	raw, err := json.Marshal(run)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO task_runs(control_id,task_id,run_id,digest,phase,boot_id,body) VALUES(?,?,?,?,?,?,?) ON CONFLICT(control_id,task_id,run_id) DO UPDATE SET phase=excluded.phase,boot_id=excluded.boot_id,body=excluded.body", run.Launch.ControlID, run.Launch.TaskID, run.Launch.RunID, run.LaunchDigest, run.Phase, run.BootID, string(raw))
	return err
}
func (j *Journal) Inspect(ctx context.Context, control, task, run string) (*Run, error) {
	value, err := readRun(ctx, j.db, control, task, run)
	if err == nil {
		err = j.hydrateRun(value)
	}
	return value, err
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
			if err := j.hydrateRun(old); err != nil {
				return err
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
				if err := j.hydrateRun(previous); err != nil {
					return err
				}
				checkpoint = previous.Context
			}
		}
		value = Run{SchemaVersion: SchemaVersion, Launch: launch, LaunchDigest: hash, BootID: j.BootID, Phase: Accepted, Context: checkpoint, Sequence: sequence, EventSequence: eventSequence}
		return j.persistRun(ctx, tx, &value)
	})(tx)
	return value, err
}
func (j *Journal) change(ctx context.Context, control, task, run string, fn func(*Run) error) error {
	return j.changeTx(ctx, control, task, run, func(_ *sql.Tx, value *Run) error { return fn(value) })
}
func (j *Journal) changeTx(ctx context.Context, control, task, run string, fn func(*sql.Tx, *Run) error) error {
	return j.changeTxTopic(ctx, control, task, run, "runs", fn)
}
func (j *Journal) changeTxTopic(ctx context.Context, control, task, run, topic string, fn func(*sql.Tx, *Run) error) error {
	changed := false
	finished := false
	err := storage.Transaction(ctx, j.db, func(tx *sql.Tx) error {
		value, err := readRun(ctx, tx, control, task, run)
		if err != nil {
			return err
		}
		if value == nil {
			return fmt.Errorf("task run not accepted")
		}
		wasEnded := value.Phase.Ended()
		before, _ := json.Marshal(value)
		if err := fn(tx, value); err != nil {
			return err
		}
		after, _ := json.Marshal(value)
		if string(before) == string(after) {
			return nil
		}
		changed = true
		finished = !wasEnded && value.Phase.Ended()
		return j.persistRun(ctx, tx, value)
	})
	if err == nil && changed {
		j.changes.Notify(topic)
		if finished {
			j.notifyFinished()
		}
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
	finished := false
	err = storage.Transaction(ctx, j.db, func(tx *sql.Tx) error {
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
		value.FinishedAtMS = time.Now().UnixMilli()
		value.Result = &result
		value.CleanupConfirmed = true
		if err := j.persistRun(ctx, tx, value); err != nil {
			return err
		}
		finished = true
		return nil
	})
	if err == nil && finished {
		j.notifyFinished()
	}
	return err
}
func (j *Journal) Wait(ctx context.Context, control, task, run, detail string) error {
	return j.changeTx(ctx, control, task, run, func(tx *sql.Tx, value *Run) error {
		if value.Phase != Running || value.BootID != j.BootID {
			return fmt.Errorf("run is fenced")
		}
		if err := j.hydrateRun(value); err != nil {
			return err
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

		// Context was loaded only to validate the waiting boundary.
		// Preserve its committed reference without adding another snapshot.
		if value.RolloutRef != nil {
			value.Context = nil
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

		if err := j.hydrateRun(value); err != nil {
			return err
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
		value.FinishedAtMS = time.Now().UnixMilli()
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
