package schedule

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jjyr/pot/internal/tasks"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

// PrepareOutcome joins schedule history and the occurrence's notification policy
// to the ordinary Task outcome. The worker's final reply remains unchanged.
func (s *Service) PrepareOutcome(ctx context.Context, task tasks.Record, event protocol.TaskResultEvent) (tasks.OutcomeCommit, error) {
	if task.Origin.Kind != "schedule" {
		return nil, nil
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
		run.State = string(event.Phase)
		run.Summary = event.Text
		failed := event.Phase == protocol.TaskFailed || event.Phase == protocol.TaskInterrupted
		notify := run.Notify == "all" || run.Notify == "failures" && failed
		if !notify {
			event.SuppressionReason = "notification_policy:" + run.Notify
		}
		if err := writeRun(ctx, tx, task.AssistantID, task.Origin.ScheduleID, *run); err != nil {
			return err
		}
		return writePlan(ctx, tx, task.AssistantID, *plan)
	}, nil
}
