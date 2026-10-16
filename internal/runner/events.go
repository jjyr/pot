package runner

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/internal/core"
)

type Event struct {
	Sequence uint64          `json:"sequence"`
	Event    core.AgentEvent `json:"event"`
}
type EventPage struct {
	Events      []Event `json:"events"`
	Cursor      uint64  `json:"cursor"`
	HasMore     bool    `json:"has_more"`
	Unavailable bool    `json:"unavailable"`
}

func (j *Journal) AppendEvent(ctx context.Context, control, task, run string, event core.AgentEvent) error {
	return j.changeTx(ctx, control, task, run, func(tx *sql.Tx, r *Run) error {
		if r.Phase != Running || r.BootID != j.BootID || event.Generation != r.Launch.Generation || event.TurnID != r.Launch.RunID || event.Seq == 0 {
			return errors.New("stale Runner transcript event")
		}
		if r.EventSequence >= core.MaxSafeInteger {
			return errors.New("Runner transcript cursor exhausted")
		}
		raw, err := json.Marshal(event)
		if err != nil {
			return err
		}
		if len(raw) > 2*1024*1024 {
			return errors.New("Runner event exceeds limit")
		}
		r.EventSequence++
		r.Sequence++
		_, err = tx.ExecContext(ctx, "INSERT INTO task_events(control_id,task_id,run_id,sequence,event)VALUES(?,?,?,?,?)", control, task, run, r.EventSequence, string(raw))
		return err
	})
}
func (j *Journal) Events(ctx context.Context, control, task, run string, after, limit uint64) (EventPage, error) {
	result := EventPage{Events: []Event{}, Cursor: after}
	if after > core.MaxSafeInteger {
		return result, errors.New("invalid transcript cursor")
	}
	if limit == 0 {
		limit = 100
	}
	if limit > 100 {
		return result, errors.New("transcript page exceeds100 events")
	}
	record, err := j.Inspect(ctx, control, task, run)
	if err != nil {
		return result, err
	}
	if record == nil {
		return result, errors.New("original Runner run missing")
	}
	rows, err := j.db.QueryContext(ctx, "SELECT sequence,event FROM task_events WHERE control_id=? AND task_id=? AND run_id=? AND sequence>? ORDER BY sequence LIMIT ?", control, task, run, after, limit)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	bytes := 0
	for rows.Next() {
		var event Event
		var raw []byte
		if err = rows.Scan(&event.Sequence, &raw); err != nil {
			return result, err
		}
		if bytes+len(raw) > 8*1024*1024 && len(result.Events) > 0 {
			break
		}
		bytes += len(raw)
		if err = core.DecodeJSON(raw, &event.Event); err != nil {
			return result, err
		}
		if event.Sequence != result.Cursor+1 {
			return result, errors.New("Runner transcript sequence gap")
		}
		result.Events = append(result.Events, event)
		result.Cursor = event.Sequence
	}
	result.HasMore = result.Cursor < record.EventSequence
	return result, rows.Err()
}
