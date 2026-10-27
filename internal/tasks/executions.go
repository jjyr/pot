package tasks

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

// Execution returns only caller-side Agent Link bindings. Local Pot run storage
// is an implementation detail of its receiving adapter.
func (s *Service) Execution(ctx context.Context, owner, id string) (*Record, error) {
	r, err := s.Get(ctx, owner, id)
	if err == nil && (r == nil || r.Execution == nil) {
		return nil, sql.ErrNoRows
	}
	return r, err
}

func (s *Service) AcceptExecution(ctx context.Context, record Record) error {
	err := storage.Transaction(ctx, s.db, func(tx *sql.Tx) error { return s.AcceptExecutionTx(ctx, tx, record) })
	if err == nil {
		s.Notify()
	}
	return err
}

// AcceptExecutionTx admits a caller-owned execution in the caller's Tasks
// transaction, allowing transport receipts and the catalogue to commit together.
// The caller publishes Notify only after the transaction commits.
func (s *Service) AcceptExecutionTx(ctx context.Context, tx *sql.Tx, record Record) error {
	if record.Execution == nil || record.Info.AgentLink == nil || !core.Identifier(record.AssistantID) || !core.Identifier(record.Info.ID) {
		return fmt.Errorf("invalid Task execution binding")
	}
	record.Plugins = []string{}
	record.SchemaVersion = 14
	record.Generation = 1
	record.Origin = protocol.TaskOrigin{Kind: "manual"}
	record.RunID = record.Info.RunID
	var exists bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM task_writes WHERE assistant_id=? AND request_id=?)", record.AssistantID, record.Request.RequestID).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return agentlink.ErrConflict
	}
	var count int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM tasks WHERE assistant_id=?", record.AssistantID).Scan(&count); err != nil {
		return err
	}
	if count >= s.options.MaxTasks {
		return fmt.Errorf("task total limit reached")
	}
	hash, err := core.HashJSON([]any{record.Request, record.Origin})
	if err != nil {
		return err
	}
	if err := write(ctx, tx, &record); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO task_writes(assistant_id,request_id,digest,task_id) VALUES(?,?,?,?)", record.AssistantID, record.Request.RequestID, hash, record.Info.ID)
	return err
}

// UpdateExecution serializes polling with guidance and dispositions. The original
// run reference is a compare-and-swap fence against stale backend responses.
func (s *Service) UpdateExecution(ctx context.Context, record Record, expectedRun string) (bool, error) {
	var changed bool
	err := storage.Transaction(ctx, s.db, func(tx *sql.Tx) error {
		var err error
		changed, err = s.UpdateExecutionTx(ctx, tx, record, expectedRun)
		return err
	})
	if err == nil && changed {
		s.Notify()
	}
	return changed, err
}

// UpdateExecutionTx applies the existing expected-run fence and outcome indexes
// in the same Tasks transaction as a transport event receipt.
func (s *Service) UpdateExecutionTx(ctx context.Context, tx *sql.Tx, record Record, expectedRun string) (bool, error) {
	current, err := read(ctx, tx, record.AssistantID, record.Info.ID)
	if err != nil {
		return false, err
	}
	if current == nil || current.Execution == nil || current.Execution.Receipt.RunID != expectedRun {
		return false, nil
	}
	// A delayed poll cannot reopen an already-ended execution. Continuation
	// must supply a new run identity through the original backend.
	if current.Info.Phase.Scope() == "attention" && record.Info.Phase.Scope() == "active" && record.Execution.Receipt.RunID == expectedRun {
		return false, nil
	}
	record.Info.Archived = current.Info.Archived
	record.Info.ArchiveReason = current.Info.ArchiveReason
	record.Execution.StopRequested = current.Execution.StopRequested
	record.Execution.Notified = current.Execution.Notified
	if expectedRun != "" && record.Execution.Receipt.RunID != expectedRun {
		record.Execution.Notified = false
		record.Execution.StopRequested = false
		record.Info.Archived = false
		record.Info.ArchiveReason = ""
		record.Outcome = nil
		record.Result = nil
	}
	record.RunID = record.Info.RunID
	record.Execution.ObservedAtMS = time.Now().UnixMilli()
	record.Error = record.Execution.Receipt.Error
	if record.Info.Phase.Scope() != "active" {
		kind := "success"
		if record.Info.Phase == protocol.TaskFailed {
			kind = "failed"
		}
		if record.Info.Phase == protocol.TaskCancelled || record.Info.Phase == protocol.TaskInterrupted {
			kind = "cancelled"
		}
		record.Result = &Terminal{Text: record.Execution.Receipt.Result, Outcome: core.Outcome{Kind: kind, Error: record.Error}}
		record.Outcome = &protocol.TaskResultEvent{TaskID: record.Info.ID, RunID: record.RunID, Title: record.Info.Title, Phase: record.Info.Phase, Text: record.Execution.Receipt.Result, Error: record.Error, Origin: record.Origin}
	}
	if err := write(ctx, tx, &record); err != nil {
		return false, err
	}
	return true, nil
}
func (s *Service) StopExecution(ctx context.Context, owner, id string) error {
	return s.change(ctx, owner, id, func(_ *sql.Tx, r *Record) error {
		if r.Execution == nil {
			return fmt.Errorf("Task has no Agent Link execution")
		}
		r.Execution.StopRequested = true
		return nil
	})
}
func (s *Service) NotifyExecution(ctx context.Context, owner, id, run string) error {
	return s.change(ctx, owner, id, func(_ *sql.Tx, r *Record) error {
		if r.Execution != nil && r.Execution.Receipt.RunID == run {
			r.Execution.Notified = true
		}
		return nil
	})
}

// FindRequest resolves accepted intent before consulting mutable destinations.
func (s *Service) FindRequest(ctx context.Context, owner string, request protocol.CreateTask) (*Record, error) {
	var id, hash string
	err := s.db.QueryRowContext(ctx, "SELECT task_id,digest FROM task_writes WHERE assistant_id=? AND request_id=?", owner, request.RequestID).Scan(&id, &hash)
	if err != nil {
		return nil, err
	}
	expected, err := core.HashJSON([]any{request, protocol.TaskOrigin{Kind: "manual"}})
	if err != nil {
		return nil, err
	}
	if hash != expected {
		return nil, agentlink.ErrConflict
	}
	record, err := s.Get(ctx, owner, id)
	if err == nil && record == nil {
		return nil, fmt.Errorf("Task receipt refers to missing record")
	}
	return record, err
}

// AwaitExecution preserves the last receipt when its owner cannot be observed.
func (s *Service) AwaitExecution(ctx context.Context, owner, id, run string) error {
	return s.change(ctx, owner, id, func(_ *sql.Tx, r *Record) error {
		if r.Execution != nil && r.Execution.Receipt.RunID == run && r.Info.Phase.Scope() == "active" {
			r.Info.RemoteWaiting = true
		}
		return nil
	})
}
