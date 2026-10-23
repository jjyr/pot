package schedule

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

// Tick takes an explicit clock. Occurrence identity, queue entry and next due
// time commit atomically. Task coordination finalizes results independently of
// the readiness gate for future launches.
func (s *Service) Tick(ctx context.Context, at int64) error {
	if at < 0 || uint64(at) > core.MaxSafeInteger {
		return fmt.Errorf("invalid scheduler clock")
	}
	if err := s.resumeProbes(ctx); err != nil {
		return err
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
	if !busy && !missed && plan.Info.Spec.BeforeRun == nil {
		hash, _ := core.HashJSON([]string{owner, occurrence})
		prepared, err = s.resolve(ctx, owner, plan.Info.Spec, "schedule:"+hash)
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
			if current.Info.Spec.BeforeRun != nil {
				if err := s.reserveProbeTx(ctx, tx, owner, *current, due, occurrence, "scheduled"); err != nil {
					return err
				}
			} else if _, err := s.launchTx(ctx, tx, owner, *current, due, occurrence, prepared); err != nil {
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
		err = s.resumeProbes(ctx)
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
