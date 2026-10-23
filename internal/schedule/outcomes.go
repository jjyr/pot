package schedule

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/jjyr/pot/internal/tasks"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

// PrepareOutcome reads the local result file before opening the terminal Task
// transaction. Its commit joins schedule history and policy to the same outcome.
func (s *Service) PrepareOutcome(ctx context.Context, task tasks.Record, event protocol.TaskResultEvent) (tasks.OutcomeCommit, error) {
	if task.Origin.Kind != "schedule" {
		return nil, nil
	}
	var result ResultFile
	var resultErr error
	if event.Phase == protocol.TaskComplete {
		if s.options.Results == nil || task.Info.ExecutionTarget == nil {
			resultErr = fmt.Errorf("local scheduled result reader unavailable")
		} else {
			var data []byte
			data, resultErr = s.options.Results.ReadResult(ctx, *task.Info.ExecutionTarget, task.Info.ID, ".pot-schedule-result.json", 16384)
			if resultErr == nil {
				result, resultErr = ParseResult(data)
			}
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	return func(ctx context.Context, tx *sql.Tx, event *protocol.TaskResultEvent) error {
		plan, err := readPlan(ctx, tx, task.AssistantID, task.Origin.ScheduleID)
		if err != nil {
			return err
		}
		if plan == nil {
			return fmt.Errorf("schedule missing")
		}
		run, err := readRun(ctx, tx, task.AssistantID, task.Origin.ScheduleID, task.Origin.OccurrenceID)
		if err != nil {
			return err
		}
		if run == nil || run.TaskID == nil || *run.TaskID != task.Info.ID || run.FinishedAtMS != nil {
			return fmt.Errorf("scheduled outcome no longer matches unfinished occurrence")
		}
		at := storage.Now()
		run.FinishedAtMS = &at
		if event.Phase == protocol.TaskComplete {
			if resultErr != nil {
				event.Phase = protocol.TaskFailed
				event.Error = "Structured result missing or invalid: " + resultErr.Error()
				event.Text = event.Error
			} else {
				changed := plan.Info.Baseline == nil || *plan.Info.Baseline != result.Fingerprint
				run.Changed = &changed
				event.Text = result.Summary + "\nEvidence: " + strings.Join(result.Evidence, "; ")
				event.Evidence = result.Evidence
				event.Artifacts = result.Artifacts
				if len(result.Artifacts) > 0 {
					event.Text += "\nArtifacts: " + strings.Join(result.Artifacts, ", ")
				}
				if run.Generation == plan.Generation {
					fingerprint := result.Fingerprint
					plan.Info.Baseline = &fingerprint
				}
			}
		}
		run.State = string(event.Phase)
		run.Summary = event.Text
		failed := event.Phase == protocol.TaskFailed || event.Phase == protocol.TaskInterrupted
		notify := run.Notify == "all" || run.Notify == "changes" && (run.Changed != nil && *run.Changed || failed) || run.Notify == "failures" && failed
		if !notify {
			event.SuppressionReason = "notification_policy:" + run.Notify
		}
		if err := writeRun(ctx, tx, task.AssistantID, task.Origin.ScheduleID, *run); err != nil {
			return err
		}
		return writePlan(ctx, tx, task.AssistantID, *plan)
	}, nil
}
