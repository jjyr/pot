package tasks

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

// Tick reconciles local runs. It starts each launch once after committing its
// intent; an uncertain outcome is inspected only under that same identity.
func (s *Service) Tick(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, "SELECT assistant_id,id FROM tasks WHERE phase IN ('queued','running','waiting','waiting_approval') OR EXISTS(SELECT 1 FROM task_inputs i WHERE i.assistant_id=tasks.assistant_id AND i.task_id=tasks.id AND i.state='pending' AND i.kind IN ('guidance','user_guidance')) ORDER BY created,id")
	if err != nil {
		return err
	}
	pairs := [][2]string{}
	for rows.Next() {
		var assistant, id string
		if err := rows.Scan(&assistant, &id); err != nil {
			rows.Close()
			return err
		}
		pairs = append(pairs, [2]string{assistant, id})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, pair := range pairs {
		if err := s.reconcile(ctx, pair[0], pair[1]); err != nil && !errors.Is(err, errOutcomeChanged) {
			return err
		}
	}
	return nil
}
func (s *Service) reconcile(ctx context.Context, assistant, id string) error {
	record, err := s.Get(ctx, assistant, id)
	if err != nil || record == nil {
		return err
	}
	if record.Execution != nil {
		return nil
	}
	if s.options.Executor == nil {
		return fmt.Errorf("local Task executor unavailable")
	}
	if record.Info.Phase.Scope() != "active" {
		if err := s.continuePending(ctx, assistant, id); err != nil {
			return err
		}
		record, err = s.Get(ctx, assistant, id)
		if err != nil || record == nil {
			return err
		}
		if record.Info.Phase.Scope() != "active" {
			return nil
		}
	}
	// Retired distributed records keep their original destination and evidence.
	// Never inspect or stop an unrelated local run under a historical remote ID.
	if !record.local(s.options.NodeID) {
		return s.markWaiting(ctx, assistant, id, protocol.APIError{Code: "original_execution_unavailable"})
	}
	launchNow := false
	if record.Launch == nil {
		target := core.RuntimeTarget(s.options.NodeID)
		if s.options.ValidateRequirements != nil {
			if err := s.options.ValidateRequirements(ctx, record.Request.Requirements); err != nil {
				return s.markWaiting(ctx, assistant, id, err)
			}
		}
		err = s.change(ctx, assistant, id, func(tx *sql.Tx, current *Record) error {
			if current.Launch != nil || current.Info.Phase.Scope() != "active" || current.StopMode != "" {
				return nil
			}
			if s.options.AdmitModel != nil {
				admitted, err := s.options.AdmitModel(ctx, tx, *current.Config)
				if err != nil {
					return err
				}
				if !admitted {
					return nil
				}
			}
			launch := Launch{ControlID: s.options.NodeID, AssistantID: assistant, RuntimeID: target.RuntimeID, TaskID: id, RunID: current.RunID, Generation: current.Generation, ExecutionTarget: target, Config: *current.Config, Plugins: current.Plugins, Skills: current.Request.Skills, Workspace: current.Request.Workspace, Text: current.Request.Text, InputID: current.RunInputID}
			if current.RunInputID != "" {
				launch.Text = current.RunText
			}
			if err := launch.Validate(target.RuntimeID); err != nil {
				return err
			}
			if executor, ok := s.options.Executor.(*Executor); ok {
				if _, err := executor.Journal.acceptTx(ctx, tx, launch); err != nil {
					return err
				}
			}
			current.Launch = &launch
			current.Info.ExecutionTarget = &target

			current.Info.Phase = protocol.TaskRunning
			current.Info.RemoteWaiting = false
			current.Error = ""
			launchNow = true
			return nil
		})
		if err != nil {
			return err
		}
		record, err = s.Get(ctx, assistant, id)
		if err != nil {
			return err
		}
	}
	if record.Launch == nil {
		return nil
	}
	var run *Run
	if launchNow {
		accepted, err := s.options.Executor.Launch(ctx, *record.Launch)
		if err != nil {
			return s.markWaiting(ctx, assistant, id, err)
		}
		run = &accepted
	} else {
		run, err = s.options.Executor.Inspect(ctx, s.options.NodeID, id, record.RunID)
		if err != nil {
			return s.markWaiting(ctx, assistant, id, err)
		}
		if run == nil {
			return s.markWaiting(ctx, assistant, id, fmt.Errorf("original launch receipt is not yet known; no replacement was sent"))
		}
		// Admission is already committed. Retrying a same-boot accepted run is
		// safe: Claim has a single winner, and restart fences prior-boot runs.
		if _, local := s.options.Executor.(*Executor); local && run.Phase == Accepted {
			accepted, startErr := s.options.Executor.Launch(ctx, *record.Launch)
			if startErr != nil {
				return s.markWaiting(ctx, assistant, id, startErr)
			}
			run = &accepted
		}
	}
	expected, err := record.Launch.Digest()
	if err != nil {
		return err
	}
	actual, err := run.Launch.Digest()
	if err != nil || actual != expected || run.LaunchDigest != expected {
		return fmt.Errorf("destination receipt changed immutable Task launch")
	}
	var terminal protocol.TaskPhase
	switch run.Phase {
	case Complete:
		terminal = protocol.TaskComplete
	case Failed:
		terminal = protocol.TaskFailed
	case Interrupted:
		terminal = protocol.TaskInterrupted
	case Cancelled:
		terminal = protocol.TaskCancelled
		if record.StopMode == "interrupt" {
			terminal = protocol.TaskInterrupted
		}
	}
	if terminal != "" {
		if run.Result == nil || !run.CleanupConfirmed {
			return core.ErrCleanupUnconfirmed
		}
		return s.finish(ctx, *record, terminal, run.Result, run.StartedAtMS, "")
	}
	return s.change(ctx, assistant, id, func(tx *sql.Tx, current *Record) error {
		if current.RunID != run.Launch.RunID {
			return fmt.Errorf("late run receipt rejected")
		}
		if current.Info.Phase.Scope() != "active" {
			return nil
		}
		current.Info.RemoteWaiting = false
		current.Error = ""
		if current.Info.StartedAtMS == 0 {
			current.Info.StartedAtMS = run.StartedAtMS
		}
		switch run.Phase {
		case Accepted:
			current.Info.Phase = protocol.TaskQueued
		case Running:
			current.Info.Phase = protocol.TaskRunning
			if s.options.AwaitingApproval != nil && s.options.AwaitingApproval(assistant, id, current.RunID, current.Generation) {
				current.Info.Phase = protocol.TaskWaitingApproval
			}
		case Waiting:
			current.Info.Phase = protocol.TaskWaiting
			current.Error = run.WaitDetail
		case RecoveryRequired:
			current.Info.Phase = protocol.TaskWaiting
			current.Info.RemoteWaiting = true
			current.Error = "original run requires receipt and cleanup reconciliation"
		default:
			return fmt.Errorf("unknown task run receipt phase")
		}
		return nil
	})
}
func (s *Service) markWaiting(ctx context.Context, assistant, id string, cause error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return s.change(ctx, assistant, id, func(tx *sql.Tx, r *Record) error {
		if r.Info.Phase.Scope() == "active" {
			r.Info.Phase = protocol.TaskWaiting
			r.Info.RemoteWaiting = true
			r.Error = cause.Error()
		}
		return nil
	})
}
func (s *Service) Stop(ctx context.Context, assistant, id, mode string) error {
	if mode != "interrupt" && mode != "cancel" {
		return fmt.Errorf("invalid stop mode")
	}
	for {
		record, err := s.Get(ctx, assistant, id)
		if err != nil {
			return err
		}
		if record == nil {
			return fmt.Errorf("task missing")
		}
		if record.Execution != nil {
			return agentlink.ErrUnsupported
		}
		if !record.local(s.options.NodeID) {
			return s.markWaiting(ctx, assistant, id, protocol.APIError{Code: "original_execution_unavailable"})
		}
		if record.Info.Phase.Scope() != "active" || record.Launch != nil {
			break
		}
		phase := protocol.TaskCancelled
		if mode == "interrupt" {
			phase = protocol.TaskInterrupted
		}
		err = s.finish(ctx, *record, phase, &Terminal{Outcome: core.Cancelled, Text: "Task stopped before dispatch."}, 0, mode)
		if errors.Is(err, errOutcomeChanged) {
			continue
		}
		return err
	}
	send := false
	var launch *Launch
	err := s.change(ctx, assistant, id, func(tx *sql.Tx, r *Record) error {
		if r.Info.Phase.Scope() != "active" {
			state := "retained"
			if mode == "cancel" {
				if r.Info.Phase == protocol.TaskInterrupted {
					r.Info.Phase = protocol.TaskCancelled
				}
				r.Info.Archived = true
				state = "cancelled"
			}
			r.Info.Pending = 0
			_, err := tx.ExecContext(ctx, "UPDATE task_inputs SET state=? WHERE assistant_id=? AND task_id=? AND state IN ('pending','active','retained')", state, assistant, id)
			return err
		}
		if r.StopMode != "" {
			return nil
		}
		r.Info.Paused = false
		r.StopMode = mode
		r.Finalizing = true
		launch = r.Launch
		if launch == nil {
			return errOutcomeChanged
		}
		send = true
		return nil
	})
	if err != nil || !send {
		return err
	}
	if launch.ControlID != s.options.NodeID || launch.RuntimeID != s.options.NodeID || launch.ExecutionTarget != core.RuntimeTarget(s.options.NodeID) {
		return s.markWaiting(ctx, assistant, id, protocol.APIError{Code: "original_execution_unavailable"})
	}
	if s.options.Executor == nil {
		return s.markWaiting(ctx, assistant, id, fmt.Errorf("destination unavailable during stop"))
	}
	if err := s.options.Executor.Stop(ctx, s.options.NodeID, id, launch.RunID, mode); err != nil {
		return s.markWaiting(ctx, assistant, id, err)
	}
	return nil
}

func (r Record) local(node string) bool {
	target := core.RuntimeTarget(node)
	return r.Execution == nil && r.NodeID == node &&
		(r.Info.ExecutionTarget == nil || *r.Info.ExecutionTarget == target) &&
		(r.Launch == nil || (r.Launch.ControlID == node && r.Launch.RuntimeID == node && r.Launch.ExecutionTarget == target))
}

// ResumeWaiting resumes the same confirmed allowance-waiting run. It never
// launches a replacement or changes destination/configuration after restart.
func (s *Service) ResumeWaiting(ctx context.Context, assistant, id string) error {
	if s.options.Executor == nil {
		return fmt.Errorf("local allowance resume unavailable")
	}
	record, err := s.Get(ctx, assistant, id)
	if err != nil {
		return err
	}
	if record == nil || !record.local(s.options.NodeID) || record.Info.Phase != protocol.TaskWaiting || record.Info.RemoteWaiting || record.Launch == nil || record.StopMode != "" {
		return fmt.Errorf("Task is not safely resumable")
	}
	if err := s.options.Executor.Resume(ctx, s.options.NodeID, id, record.RunID); err != nil {
		return s.markWaiting(ctx, assistant, id, err)
	}
	return s.change(ctx, assistant, id, func(tx *sql.Tx, r *Record) error { r.Info.Phase = protocol.TaskQueued; r.Error = ""; return nil })
}
func (s *Service) Notifications(ctx context.Context, assistant string) ([]Notification, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,recipient,envelope FROM agent_messages WHERE recipient=? AND direction='in' AND state='queued' AND json_extract(envelope,'$.kind')='task_result' ORDER BY sequence`, assistant)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Notification{}
	for rows.Next() {
		var n Notification
		var body []byte
		var message protocol.Envelope
		if err := rows.Scan(&n.ID, &n.AssistantID, &body); err != nil {
			return nil, err
		}
		if err := core.DecodeJSON(body, &message); err != nil {
			return nil, err
		}
		if message.Result == nil {
			return nil, fmt.Errorf("Task result missing")
		}
		n.TaskResultEvent = *message.Result
		result = append(result, n)
	}
	return result, rows.Err()
}
func (s *Service) AcknowledgeNotification(ctx context.Context, assistant, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM task_notifications WHERE assistant_id=? AND id=?", assistant, id)
	return err
}
func (s *Service) Wait(ctx context.Context, assistant string, targets []WaitTarget) ([]WaitItem, error) {
	if len(targets) == 0 || len(targets) > 8 {
		return nil, fmt.Errorf("wait requires 1–8 tasks")
	}
	seen := map[string]bool{}
	for _, target := range targets {
		if seen[target.TaskID] {
			return nil, fmt.Errorf("duplicate wait target")
		}
		seen[target.TaskID] = true
	}
	subscription := s.changes.Subscribe()
	defer subscription.Close()
	for {
		items := []WaitItem{}
		ready := false
		for _, target := range targets {
			record, err := s.Get(ctx, assistant, target.TaskID)
			if err != nil {
				return nil, err
			}
			if record == nil {
				return nil, fmt.Errorf("wait target missing")
			}
			item := WaitItem{record.Info, record.Revision, record.Error}
			items = append(items, item)
			if record.Revision > target.After && (record.Info.Phase == protocol.TaskWaiting || record.Info.Phase == protocol.TaskWaitingApproval || record.Info.Phase.Scope() != "active") {
				ready = true
			}
		}
		if ready {
			return items, nil
		}
		if _, err := subscription.Next(ctx); err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return items, nil
			}
			return nil, err
		}
	}
}
