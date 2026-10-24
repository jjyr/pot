package tasks

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/jjyr/pot/pkg/protocol"
)

var errOutcomeChanged = errors.New("Task changed during outcome preparation")

func outcome(record Record, phase protocol.TaskPhase, result *Terminal) protocol.TaskResultEvent {
	text, diagnostic := record.Error, record.Error
	if result != nil {
		diagnostic = result.Outcome.Error
		text = result.Text
	}
	if diagnostic != "" && diagnostic != text {
		if text != "" {
			text += "\n\n"
		}
		text += "Execution error: " + diagnostic
	}
	if strings.TrimSpace(text) == "" {
		text = "Task ended with status " + string(phase) + "."
	}
	return protocol.TaskResultEvent{TaskID: record.Info.ID, RunID: record.RunID, Title: record.Info.Title, Phase: phase, Text: text, Error: diagnostic, Origin: record.Origin}
}

// finish prepares domain data outside the transaction. A stale snapshot cannot
// commit a result or enqueue a hook for a changed run.
func (s *Service) finish(ctx context.Context, snapshot Record, phase protocol.TaskPhase, result *Terminal, started, finished int64, stopMode string) error {
	event := outcome(snapshot, phase, result)
	var commit OutcomeCommit
	var err error
	if s.options.PrepareOutcome != nil {
		commit, err = s.options.PrepareOutcome(ctx, snapshot, event)
		if err != nil {
			return err
		}
	}
	if snapshot.Origin.Kind == "schedule" && commit == nil {
		return fmt.Errorf("scheduled Task outcome processor unavailable")
	}
	return s.change(ctx, snapshot.AssistantID, snapshot.Info.ID, func(tx *sql.Tx, current *Record) error {
		if current.Info.Phase.Scope() != "active" {
			return nil
		}
		if current.RunID != snapshot.RunID || current.Generation != snapshot.Generation || current.Revision != snapshot.Revision {
			return errOutcomeChanged
		}
		if commit != nil {
			if err := commit(ctx, tx, &event); err != nil {
				return err
			}
		}
		if event.TaskID != current.Info.ID || event.RunID != current.RunID || event.Origin != current.Origin {
			return fmt.Errorf("prepared outcome changed Task identity or origin")
		}
		switch event.Phase {
		case protocol.TaskComplete, protocol.TaskFailed, protocol.TaskInterrupted, protocol.TaskCancelled:
		default:
			return fmt.Errorf("prepared Task outcome is not terminal")
		}
		current.Info.Phase = event.Phase
		current.Info.FinishedAtMS = finished
		current.Info.Paused = false
		current.Info.RemoteWaiting = false
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM task_inputs WHERE assistant_id=? AND task_id=? AND state='pending' AND kind IN ('guidance','user_guidance')", current.AssistantID, current.Info.ID).Scan(&current.Info.Pending); err != nil {
			return err
		}
		current.Info.Archived = current.StopMode == "cancel" || stopMode == "cancel"
		if current.Info.StartedAtMS == 0 {
			current.Info.StartedAtMS = started
		}
		if stopMode != "" {
			current.StopMode = stopMode
		}
		current.Finalizing = true
		current.Result = result
		current.Error = event.Error
		current.Outcome = &event
		state := "retained"
		if current.StopMode == "cancel" {
			state = "cancelled"
		}
		if _, err := tx.ExecContext(ctx, "UPDATE task_inputs SET state=? WHERE assistant_id=? AND task_id=? AND state='pending' AND (kind='reference' OR ?!='')", state, current.AssistantID, current.Info.ID, current.StopMode); err != nil {
			return err
		}
		if current.StopMode != "" {
			current.Info.Pending = 0
		}
		if stopMode != "" {
			_, err := tx.ExecContext(ctx, "UPDATE task_inputs SET state=? WHERE assistant_id=? AND task_id=? AND state='active'", state, current.AssistantID, current.Info.ID)
			return err
		}
		return nil
	})
}
