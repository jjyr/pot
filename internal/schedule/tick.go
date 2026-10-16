package schedule

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/jjyr/pot/internal/core"
	"github.com/jjyr/pot/internal/protocol"
	"github.com/jjyr/pot/internal/storage"
	"github.com/jjyr/pot/internal/tasks"
	"strings"
)

func ParseResult(data []byte) (ResultFile, error) {
	if len(data) > 16384 {
		return ResultFile{}, fmt.Errorf("result exceeds 16 KiB")
	}
	var result ResultFile
	if err := core.DecodeJSON(data, &result); err != nil {
		return result, err
	}
	if strings.TrimSpace(result.Summary) == "" || len(result.Summary) > 4000 || strings.TrimSpace(result.Fingerprint) == "" || len(result.Fingerprint) > 2048 || len(result.Evidence) == 0 || len(result.Evidence) > 16 || len(result.Artifacts) > 16 {
		return result, fmt.Errorf("result needs bounded summary, stable fingerprint and evidence")
	}
	for _, evidence := range result.Evidence {
		if strings.TrimSpace(evidence) == "" || len(evidence) > 1000 {
			return result, fmt.Errorf("invalid result evidence")
		}
	}
	for _, path := range result.Artifacts {
		if !protocol.ValidFilePath(path) || len(path) > 1000 {
			return result, fmt.Errorf("invalid artifact path")
		}
	}
	return result, nil
}
func (s *Service) unfinished(ctx context.Context) ([]queued, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT assistant_id,schedule_id,body FROM schedule_runs WHERE finished IS NULL ORDER BY scheduled,rowid")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []queued{}
	for rows.Next() {
		var q queued
		var raw string
		if err := rows.Scan(&q.Owner, &q.PlanID, &raw); err != nil {
			return nil, err
		}
		if err := core.DecodeJSON([]byte(raw), &q.Run); err != nil {
			return nil, err
		}
		result = append(result, q)
	}
	return result, rows.Err()
}

// Tick takes an explicit clock. Occurrence identity, queue entry and next due
// time commit atomically. Readiness gates future launches, not result recovery.
func (s *Service) Tick(ctx context.Context, at int64) error {
	if at < 0 || uint64(at) > core.MaxSafeInteger {
		return fmt.Errorf("invalid scheduler clock")
	}
	pending, err := s.unfinished(ctx)
	if err != nil {
		return err
	}
	for _, q := range pending {
		if q.Run.TaskID == nil {
			return fmt.Errorf("unfinished schedule occurrence has no Task")
		}
		task, err := s.options.Tasks.Get(ctx, q.Owner, *q.Run.TaskID)
		if err != nil {
			return err
		}
		if task == nil {
			return fmt.Errorf("scheduled Task disappeared")
		}
		if task.Info.Phase.Scope() == "active" {
			continue
		}
		var result ResultFile
		var resultErr error
		if task.Info.Phase == protocol.TaskComplete {
			if s.options.Results == nil || task.Info.ExecutionTarget == nil {
				resultErr = fmt.Errorf("fixed destination result transport unavailable")
			} else {
				data, err := s.options.Results.ReadResult(ctx, *task.Info.ExecutionTarget, task.Info.ID, ".pot-schedule-result.json", 16384)
				if err != nil {
					resultErr = err
				} else {
					result, resultErr = ParseResult(data)
				}
			}
		}
		if errors.Is(resultErr, tasks.ErrResultPending) {
			continue
		}
		if err := s.complete(ctx, q, task.Info.Phase, result, resultErr, at); err != nil {
			return err
		}
	}
	rows, err := s.db.QueryContext(ctx, "SELECT assistant_id,id FROM schedules ORDER BY rowid")
	if err != nil {
		return err
	}
	pairs := [][2]string{}
	for rows.Next() {
		var owner, id string
		if err := rows.Scan(&owner, &id); err != nil {
			rows.Close()
			return err
		}
		pairs = append(pairs, [2]string{owner, id})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	readiness := map[string]bool{}
	for _, pair := range pairs {
		plan, err := s.Get(ctx, pair[0], pair[1])
		if err != nil {
			return err
		}
		if plan == nil || plan.Info.Deleted || !plan.Info.Spec.Enabled || plan.Info.NextAtMS == nil || *plan.Info.NextAtMS > at {
			continue
		}
		ready, known := readiness[pair[0]]
		if !known {
			ready, err = s.options.Ready(ctx, pair[0])
			if err != nil {
				return err
			}
			readiness[pair[0]] = ready
		}
		if !ready {
			continue
		}
		if err := s.trigger(ctx, pair[0], *plan, at); err != nil {
			return err
		}
	}
	return nil
}
func (s *Service) trigger(ctx context.Context, owner string, plan Plan, at int64) error {
	due := *plan.Info.NextAtMS
	occurrence := fmt.Sprintf("%s:%d", plan.Info.ID, due)
	var busy bool
	err := storage.Transaction(ctx, s.db, func(tx *sql.Tx) error { var err error; busy, err = s.busyTx(ctx, tx, owner, plan.Info.ID); return err })
	if err != nil {
		return err
	}
	missed := at-due >= 60000 && plan.Info.Spec.Misfire == "skip"
	var prepared resolved
	if !busy && !missed {
		hash, _ := core.HashJSON([]string{owner, occurrence})
		prepared, err = s.resolve(ctx, owner, plan.Info.Spec, "schedule:"+hash, plan.Info.Baseline)
		if err != nil {
			return s.lastError(ctx, owner, plan.Info.ID, err.Error())
		}
	}
	expected, _ := core.HashJSON(plan)
	err = storage.Transaction(ctx, s.db, func(tx *sql.Tx) error {
		current, err := readPlan(ctx, tx, owner, plan.Info.ID)
		if err != nil {
			return err
		}
		if current == nil {
			return fmt.Errorf("schedule missing")
		}
		actual, _ := core.HashJSON(current)
		if actual != expected {
			return nil
		}
		if old, err := readRun(ctx, tx, owner, plan.Info.ID, occurrence); err != nil {
			return err
		} else if old != nil {
			return nil
		}
		busyNow, err := s.busyTx(ctx, tx, owner, plan.Info.ID)
		if err != nil {
			return err
		}
		next, err := AfterDue(current.Info.Spec, due, at)
		if err != nil {
			return err
		}
		if busyNow || missed {
			state := "skipped_missed"
			if busyNow {
				state = "skipped_overlap"
			}
			run := protocol.ScheduleRun{ID: occurrence, ScheduledForMS: due, Generation: current.Generation, State: state, Summary: "No Task or model call was created.", FinishedAtMS: &at, Notify: current.Info.Spec.Notify}
			if err := writeRun(ctx, tx, owner, current.Info.ID, run); err != nil {
				return err
			}
		} else {
			if busy && !busyNow {
				return nil
			}
			if _, err := s.launchTx(ctx, tx, owner, *current, due, occurrence, prepared); err != nil {
				return err
			}
		}
		current.Info.NextAtMS = next
		if next == nil {
			current.Info.Spec.Enabled = false
		}
		current.Info.LastError = ""
		return writePlan(ctx, tx, owner, *current)
	})
	if err == nil {
		s.options.Tasks.Notify()
	}
	return err
}
func (s *Service) lastError(ctx context.Context, owner, id, detail string) error {
	return storage.Transaction(ctx, s.db, func(tx *sql.Tx) error {
		plan, err := readPlan(ctx, tx, owner, id)
		if err != nil {
			return err
		}
		if plan == nil {
			return fmt.Errorf("schedule missing")
		}
		if plan.Info.LastError == detail {
			return nil
		}
		plan.Info.LastError = detail
		return writePlan(ctx, tx, owner, *plan)
	})
}
func (s *Service) complete(ctx context.Context, q queued, phase protocol.TaskPhase, result ResultFile, resultErr error, at int64) error {
	err := storage.Transaction(ctx, s.db, func(tx *sql.Tx) error {
		plan, err := readPlan(ctx, tx, q.Owner, q.PlanID)
		if err != nil {
			return err
		}
		if plan == nil {
			return fmt.Errorf("schedule missing")
		}
		run, err := readRun(ctx, tx, q.Owner, q.PlanID, q.Run.ID)
		if err != nil {
			return err
		}
		if run == nil {
			return fmt.Errorf("schedule occurrence missing")
		}
		if run.FinishedAtMS != nil {
			return nil
		}
		task, err := s.options.Tasks.GetTx(ctx, tx, q.Owner, *run.TaskID)
		if err != nil {
			return err
		}
		if task == nil || task.Info.Phase.Scope() == "active" {
			return fmt.Errorf("late scheduled result no longer matches ended Task")
		}
		phase = task.Info.Phase
		run.FinishedAtMS = &at
		if phase == protocol.TaskComplete {
			if resultErr != nil {
				phase = protocol.TaskFailed
				run.Summary = "Structured result missing or invalid: " + resultErr.Error()
				if err := s.options.Tasks.FailResultTx(ctx, tx, q.Owner, task.Info.ID, run.Summary); err != nil {
					return err
				}
			} else {
				changed := plan.Info.Baseline == nil || *plan.Info.Baseline != result.Fingerprint
				run.Changed = &changed
				run.Summary = result.Summary + "\nEvidence: " + strings.Join(result.Evidence, "; ")
				if len(result.Artifacts) > 0 {
					run.Summary += "\nArtifacts: " + strings.Join(result.Artifacts, ", ")
				}
				if run.Generation == plan.Generation {
					fingerprint := result.Fingerprint
					plan.Info.Baseline = &fingerprint
				}
			}
		} else if task.Result != nil {
			run.Summary = task.Result.Text
			if len([]rune(run.Summary)) > 1200 {
				run.Summary = string([]rune(run.Summary)[:1200])
			}
		}
		run.State = string(phase)
		failed := phase == protocol.TaskFailed || phase == protocol.TaskInterrupted
		notify := run.Notify == "all" || run.Notify == "changes" && (run.Changed != nil && *run.Changed || failed) || run.Notify == "failures" && failed
		if notify {
			id, _ := core.HashJSON([]string{q.Owner, q.PlanID, q.Run.ID})
			text := fmt.Sprintf("Schedule %q: %s\n%s", plan.Info.Spec.Title, run.State, run.Summary)
			if _, err := tx.ExecContext(ctx, "INSERT INTO schedule_notifications(id,assistant_id,schedule_id,run_id,text) VALUES(?,?,?,?,?) ON CONFLICT(id) DO NOTHING", id, q.Owner, q.PlanID, q.Run.ID, text); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM task_notifications WHERE assistant_id=? AND task_id=?", q.Owner, task.Info.ID); err != nil {
			return err
		}
		if err := writeRun(ctx, tx, q.Owner, q.PlanID, *run); err != nil {
			return err
		}
		return writePlan(ctx, tx, q.Owner, *plan)
	})
	if err == nil {
		s.options.Tasks.Notify()
	}
	return err
}
func (s *Service) Notifications(ctx context.Context, owner string) ([]Notification, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id,assistant_id,schedule_id,run_id,text FROM schedule_notifications WHERE assistant_id=? ORDER BY rowid", owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Notification{}
	for rows.Next() {
		var n Notification
		if err := rows.Scan(&n.ID, &n.AssistantID, &n.ScheduleID, &n.RunID, &n.Text); err != nil {
			return nil, err
		}
		result = append(result, n)
	}
	return result, rows.Err()
}
func (s *Service) AcknowledgeNotification(ctx context.Context, owner, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM schedule_notifications WHERE assistant_id=? AND id=?", owner, id)
	return err
}
