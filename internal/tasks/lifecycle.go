package tasks

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/jjyr/pot/internal/runner"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

// Tick reconciles original runs. It sends each launch once after committing its
// intent; an uncertain transport is only inspected under that same identity.
func (s *Service) Tick(ctx context.Context) error {
	if s.options.Port == nil || s.options.ResolveTarget == nil {
		return fmt.Errorf("Task destination ports unavailable")
	}
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
	launchNow := false
	if record.Launch == nil {
		target, err := s.options.ResolveTarget(ctx, record.Request.MachineID)
		if err == nil && record.Info.ExecutionTarget != nil && target != *record.Info.ExecutionTarget {
			err = fmt.Errorf("original Task execution target changed")
		}
		if err != nil {
			return s.markWaiting(ctx, assistant, id, err)
		}
		if target.RuntimeID != record.Request.MachineID {
			return fmt.Errorf("destination policy resolved another machine")
		}
		if err := target.Validate(); err != nil {
			return err
		}
		if s.options.ValidateTarget != nil {
			if err := s.options.ValidateTarget(ctx, target, record.Request.Requirements); err != nil {
				return s.markWaiting(ctx, assistant, id, err)
			}
		}
		err = s.change(ctx, assistant, id, func(tx *sql.Tx, current *Record) error {
			if current.Launch != nil || current.Info.Phase.Scope() != "active" || current.StopMode != "" {
				return nil
			}
			if s.options.AdmitModel != nil {
				admitted, err := s.options.AdmitModel(ctx, tx, current.Config)
				if err != nil {
					return err
				}
				if !admitted {
					return nil
				}
			}
			launch := runner.Launch{ControlID: s.options.ControlID, AssistantID: assistant, RuntimeID: target.RuntimeID, TaskID: id, RunID: current.RunID, Generation: current.Generation, ExecutionTarget: target, Config: current.Config, Plugins: current.Plugins, Skills: current.Request.Skills, Workspace: current.Request.Workspace, Text: current.Request.Text, InputID: current.RunInputID}
			if current.RunInputID != "" {
				launch.Text = current.RunText
			}
			if err := launch.Validate(target.RuntimeID); err != nil {
				return err
			}
			current.Launch = &launch
			current.LaunchSent = true
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
	var run *runner.Run
	if launchNow {
		accepted, err := s.options.Port.Launch(ctx, *record.Launch)
		if err != nil {
			return s.markWaiting(ctx, assistant, id, err)
		}
		run = &accepted
	} else {
		run, err = s.options.Port.Inspect(ctx, s.options.ControlID, id, record.RunID)
		if err != nil {
			return s.markWaiting(ctx, assistant, id, err)
		}
		if run == nil {
			return s.markWaiting(ctx, assistant, id, fmt.Errorf("original launch receipt is not yet known; no replacement was sent"))
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
	case runner.Complete:
		terminal = protocol.TaskComplete
	case runner.Failed:
		terminal = protocol.TaskFailed
	case runner.Interrupted:
		terminal = protocol.TaskInterrupted
	case runner.Cancelled:
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
		case runner.Accepted:
			current.Info.Phase = protocol.TaskQueued
		case runner.Running:
			current.Info.Phase = protocol.TaskRunning
			if s.options.AwaitingApproval != nil && s.options.AwaitingApproval(assistant, id, current.RunID, current.Generation) {
				current.Info.Phase = protocol.TaskWaitingApproval
			}
		case runner.Waiting:
			current.Info.Phase = protocol.TaskWaiting
			current.Error = run.WaitDetail
		case runner.RecoveryRequired:
			current.Info.Phase = protocol.TaskWaiting
			current.Info.RemoteWaiting = true
			current.Error = "original run requires receipt and cleanup reconciliation"
		default:
			return fmt.Errorf("unknown Runner receipt phase")
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
		if record.Info.Phase.Scope() != "active" || record.Launch != nil {
			break
		}
		phase := protocol.TaskCancelled
		if mode == "interrupt" {
			phase = protocol.TaskInterrupted
		}
		err = s.finish(ctx, *record, phase, &runner.Terminal{Outcome: core.Cancelled, Text: "Task stopped before dispatch."}, 0, mode)
		if errors.Is(err, errOutcomeChanged) {
			continue
		}
		return err
	}
	send := false
	var launch *runner.Launch
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
	if s.options.Port == nil {
		return s.markWaiting(ctx, assistant, id, fmt.Errorf("destination unavailable during stop"))
	}
	if err := s.options.Port.Stop(ctx, s.options.ControlID, id, launch.RunID, mode); err != nil {
		return s.markWaiting(ctx, assistant, id, err)
	}
	return nil
}

// ResumeWaiting resumes the same confirmed allowance-waiting run. It never
// launches a replacement or changes destination/configuration after restart.
func (s *Service) ResumeWaiting(ctx context.Context, assistant, id string) error {
	port, ok := s.options.Port.(Resumer)
	if !ok {
		return fmt.Errorf("destination allowance resume port unavailable")
	}
	record, err := s.Get(ctx, assistant, id)
	if err != nil {
		return err
	}
	if record == nil || record.Info.Phase != protocol.TaskWaiting || record.Info.RemoteWaiting || record.Launch == nil || record.StopMode != "" {
		return fmt.Errorf("Task is not safely resumable")
	}
	if err := port.Resume(ctx, s.options.ControlID, id, record.RunID); err != nil {
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
