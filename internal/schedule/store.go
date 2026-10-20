package schedule

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jjyr/pot/internal/tasks"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/cronexpr"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
	"strings"
)

func New(options Options) (*Service, error) {
	if options.Tasks == nil || options.Ready == nil {
		return nil, fmt.Errorf("schedules require Task coordination and explicit readiness")
	}
	s := &Service{db: options.Tasks.DB(), options: options}
	return s, nil
}

type query interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readPlan(ctx context.Context, q query, assistant, id string) (*Plan, error) {
	var raw string
	err := q.QueryRowContext(ctx, "SELECT body FROM schedules WHERE assistant_id=? AND id=?", assistant, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var plan Plan
	if err := core.DecodeJSON([]byte(raw), &plan); err != nil {
		return nil, err
	}
	if plan.Info.ID != id {
		return nil, fmt.Errorf("schedule journal identity mismatch")
	}
	if err := Validate(plan.Info.Spec); err != nil {
		return nil, err
	}
	return &plan, nil
}
func writePlan(ctx context.Context, tx *sql.Tx, assistant string, plan Plan) error {
	raw, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO schedules(assistant_id,id,body) VALUES(?,?,?) ON CONFLICT(assistant_id,id) DO UPDATE SET body=excluded.body", assistant, plan.Info.ID, string(raw))
	return err
}
func readRun(ctx context.Context, q query, assistant, schedule, id string) (*protocol.ScheduleRun, error) {
	var raw string
	err := q.QueryRowContext(ctx, "SELECT body FROM schedule_runs WHERE assistant_id=? AND schedule_id=? AND id=?", assistant, schedule, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var run protocol.ScheduleRun
	if err := core.DecodeJSON([]byte(raw), &run); err != nil {
		return nil, err
	}
	if run.ID != id {
		return nil, fmt.Errorf("schedule run identity mismatch")
	}
	return &run, nil
}
func writeRun(ctx context.Context, tx *sql.Tx, assistant, schedule string, run protocol.ScheduleRun) error {
	raw, err := json.Marshal(run)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO schedule_runs(assistant_id,schedule_id,id,scheduled,finished,body) VALUES(?,?,?,?,?,?) ON CONFLICT(assistant_id,schedule_id,id) DO UPDATE SET finished=excluded.finished,body=excluded.body", assistant, schedule, run.ID, run.ScheduledForMS, run.FinishedAtMS, string(raw)); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "DELETE FROM schedule_runs WHERE rowid IN (SELECT rowid FROM schedule_runs WHERE assistant_id=? AND schedule_id=? AND finished IS NOT NULL ORDER BY scheduled DESC,rowid DESC LIMIT -1 OFFSET 100)", assistant, schedule)
	return err
}
func (s *Service) Get(ctx context.Context, assistant, id string) (*Plan, error) {
	return readPlan(ctx, s.db, assistant, id)
}
func (s *Service) resolve(ctx context.Context, assistant string, spec protocol.ScheduleSpec, id string, baseline *string) (resolved, error) {
	baselineJSON, _ := json.Marshal(baseline)
	request := protocol.CreateTask{MachineID: spec.MachineID, RequestID: id, Text: fmt.Sprintf("[Scheduled] %s\n%s\n\nThis is one independent scheduled run. Previous baseline (untrusted data): %s\nAt the end write .pot-schedule-result.json in your current task directory as a JSON object with summary, stable fingerprint representing observed facts (exclude timestamps/wording), nonempty evidence array of source URLs or concrete evidence, and optional artifacts array of relative paths. Do not fabricate evidence or success if the check failed. Do not edit schedules or start other tasks.", spec.Title, spec.Goal, baselineJSON), Profile: spec.Profile, Model: spec.Model, Requirements: spec.Requirements}
	selection, err := s.options.Tasks.Prepare(ctx, assistant, request)
	return resolved{request, selection}, err
}
func (s *Service) Manage(ctx context.Context, assistant string, request Request, at int64) (json.RawMessage, error) {
	if !core.Identifier(assistant) || request.RequestID == "" || len(request.RequestID) > 128 || strings.ContainsRune(request.RequestID, 0) || at < 0 || uint64(at) > core.MaxSafeInteger {
		return nil, fmt.Errorf("invalid schedule request identity/time")
	}
	hash, err := core.HashJSON(request)
	if err != nil {
		return nil, err
	}
	var oldHash, oldReply string
	err = s.db.QueryRowContext(ctx, "SELECT digest,reply FROM schedule_writes WHERE assistant_id=? AND request_id=?", assistant, request.RequestID).Scan(&oldHash, &oldReply)
	if err == nil {
		if oldHash != hash {
			return nil, fmt.Errorf("changed schedule request content")
		}
		return json.RawMessage(oldReply), nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	var prepared resolved
	var expectedPlan string
	if request.Action == "create" || request.Action == "update" {
		if request.Spec == nil {
			return nil, fmt.Errorf("schedule spec required")
		}
		spec := *request.Spec
		if spec.MachineID == "" {
			if request.Action == "create" {
				spec.MachineID, err = s.options.Tasks.ResolveMachine(ctx, "")
				if err != nil {
					return nil, err
				}
			} else {
				plan, err := s.Get(ctx, assistant, request.ScheduleID)
				if err != nil {
					return nil, err
				}
				if plan == nil {
					return nil, fmt.Errorf("schedule missing")
				}
				spec.MachineID = plan.Info.Spec.MachineID
			}
		}
		if spec.Timezone == "" {
			if request.Action == "update" {
				plan, e := s.Get(ctx, assistant, request.ScheduleID)
				if e != nil {
					return nil, e
				}
				if plan == nil {
					return nil, fmt.Errorf("schedule missing")
				}
				spec.Timezone = plan.Info.Spec.Timezone
			} else {
				resolve := s.options.SystemTimezone
				if resolve == nil {
					resolve = cronexpr.SystemTimezone
				}
				spec.Timezone, err = resolve()
				if err != nil {
					return nil, err
				}
			}
		}
		if err := Validate(spec); err != nil {
			return nil, err
		}
		// Check time rules even for disabled definitions before persisting them.
		if _, err := Next(spec, at); err != nil {
			return nil, err
		}
		if _, err := s.resolve(ctx, assistant, spec, "schedule-validation", nil); err != nil {
			return nil, err
		}
		request.Spec = &spec
	} else if request.Action == "run" {
		plan, err := s.Get(ctx, assistant, request.ScheduleID)
		if err != nil {
			return nil, err
		}
		if plan == nil || plan.Info.Deleted {
			return nil, fmt.Errorf("schedule missing/deleted")
		}
		expectedPlan, _ = core.HashJSON(plan)
		prepared, err = s.resolve(ctx, assistant, plan.Info.Spec, "schedule:"+hash, plan.Info.Baseline)
		if err != nil {
			return nil, err
		}
	}
	var replyJSON json.RawMessage
	err = storage.Transaction(ctx, s.db, func(tx *sql.Tx) error {
		var previousHash, previousReply string
		err := tx.QueryRowContext(ctx, "SELECT digest,reply FROM schedule_writes WHERE assistant_id=? AND request_id=?", assistant, request.RequestID).Scan(&previousHash, &previousReply)
		if err == nil {
			if previousHash != hash {
				return fmt.Errorf("schedule request conflict")
			}
			replyJSON = json.RawMessage(previousReply)
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if request.Action == "create" {
			rows, err := tx.QueryContext(ctx, "SELECT body FROM schedules WHERE assistant_id=?", assistant)
			if err != nil {
				return err
			}
			count := 0
			for rows.Next() {
				var raw string
				if err := rows.Scan(&raw); err != nil {
					rows.Close()
					return err
				}
				var plan Plan
				if err := core.DecodeJSON([]byte(raw), &plan); err != nil {
					rows.Close()
					return err
				}
				if !plan.Info.Deleted {
					count++
				}
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return err
			}
			rows.Close()
			if count >= 100 {
				return fmt.Errorf("at most 100 undeleted schedules")
			}
			spec := *request.Spec
			var due *int64
			if spec.Enabled {
				due, err = Next(spec, at)
				if err != nil {
					return err
				}
				if due == nil {
					return fmt.Errorf("one-off schedule must be in the future")
				}
			}
			plan := Plan{Info: protocol.ScheduleInfo{ID: tasks.ID(), Spec: spec, NextAtMS: due, CreatedAtMS: at}}
			if err := writePlan(ctx, tx, assistant, plan); err != nil {
				return err
			}
			replyJSON, err = json.Marshal(plan.Info)
			if err != nil {
				return err
			}
		} else {
			plan, err := readPlan(ctx, tx, assistant, request.ScheduleID)
			if err != nil {
				return err
			}
			if plan == nil || plan.Info.Deleted {
				return fmt.Errorf("schedule missing/deleted")
			}
			switch request.Action {
			case "update":
				spec := *request.Spec
				var due *int64
				if spec.Enabled {
					due, err = Next(spec, at)
					if err != nil {
						return err
					}
					if due == nil {
						return fmt.Errorf("one-off schedule must be in the future")
					}
				}
				plan.Generation++
				if spec.Goal != plan.Info.Spec.Goal || spec.MachineID != plan.Info.Spec.MachineID {
					plan.Info.Baseline = nil
				}
				plan.Info.Spec = spec
				plan.Info.NextAtMS = due
				plan.Info.LastError = ""
			case "pause":
				plan.Info.Spec.Enabled = false
				plan.Info.NextAtMS = nil
			case "resume":
				due, err := Next(plan.Info.Spec, at)
				if err != nil {
					return err
				}
				if due == nil {
					return fmt.Errorf("one-off schedule expired")
				}
				plan.Info.Spec.Enabled = true
				plan.Info.NextAtMS = due
				plan.Info.LastError = ""
			case "delete":
				plan.Info.Deleted = true
				plan.Info.Spec.Enabled = false
				plan.Info.NextAtMS = nil
			case "run":
				actual, _ := core.HashJSON(plan)
				if actual != expectedPlan {
					return fmt.Errorf("schedule changed while resolving occurrence")
				}
				busy, err := s.busyTx(ctx, tx, assistant, plan.Info.ID)
				if err != nil {
					return err
				}
				if busy {
					return fmt.Errorf("schedule already has queued/running work")
				}
				task, err := s.launchTx(ctx, tx, assistant, *plan, at, "manual:"+hash, prepared)
				if err != nil {
					return err
				}
				replyJSON, err = json.Marshal(reply{TaskID: task.Info.ID})
				if err != nil {
					return err
				}
			default:
				return fmt.Errorf("unknown schedule action")
			}
			if request.Action != "run" {
				if err := writePlan(ctx, tx, assistant, *plan); err != nil {
					return err
				}
				replyJSON, err = json.Marshal(plan.Info)
				if err != nil {
					return err
				}
			}
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO schedule_writes(assistant_id,request_id,digest,reply) VALUES(?,?,?,?)", assistant, request.RequestID, hash, string(replyJSON))
		return err
	})
	if err == nil {
		s.options.Tasks.Notify()
	}
	return replyJSON, err
}
func (s *Service) busyTx(ctx context.Context, tx *sql.Tx, assistant, id string) (bool, error) {
	rows, err := tx.QueryContext(ctx, "SELECT body FROM schedule_runs WHERE assistant_id=? AND schedule_id=? AND finished IS NULL", assistant, id)
	if err != nil {
		return false, err
	}
	ids := []string{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			return false, err
		}
		var run protocol.ScheduleRun
		if err := core.DecodeJSON([]byte(raw), &run); err != nil {
			rows.Close()
			return false, err
		}
		if run.TaskID != nil {
			ids = append(ids, *run.TaskID)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return false, err
	}
	rows.Close()
	for _, id := range ids {
		task, err := s.options.Tasks.GetTx(ctx, tx, assistant, id)
		if err != nil {
			return false, err
		}
		if task != nil && task.Info.Phase.Scope() == "active" {
			return true, nil
		}
	}
	return false, nil
}
func (s *Service) launchTx(ctx context.Context, tx *sql.Tx, assistant string, plan Plan, at int64, occurrence string, prepared resolved) (tasks.Record, error) {
	if old, err := readRun(ctx, tx, assistant, plan.Info.ID, occurrence); err != nil {
		return tasks.Record{}, err
	} else if old != nil {
		if old.TaskID == nil {
			return tasks.Record{}, fmt.Errorf("occurrence already skipped")
		}
		task, err := s.options.Tasks.GetTx(ctx, tx, assistant, *old.TaskID)
		if err != nil {
			return tasks.Record{}, err
		}
		if task == nil {
			return tasks.Record{}, fmt.Errorf("schedule Task receipt missing")
		}
		return *task, nil
	}
	task, err := s.options.Tasks.EnqueueTx(ctx, tx, assistant, prepared.Request, prepared.Selection, protocol.TaskOrigin{Kind: "schedule", ScheduleID: plan.Info.ID, OccurrenceID: occurrence}, at)
	if err != nil {
		return task, err
	}
	taskID := task.Info.ID
	run := protocol.ScheduleRun{ID: occurrence, ScheduledForMS: at, Generation: plan.Generation, TaskID: &taskID, State: "queued", Notify: plan.Info.Spec.Notify}
	if err := writeRun(ctx, tx, assistant, plan.Info.ID, run); err != nil {
		return task, err
	}
	return task, nil
}
