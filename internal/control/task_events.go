package control

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/internal/runner"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/storage"
)

func (s *Service) TaskEvents(ctx context.Context, owner, task string, after, limit uint64) (runner.EventPage, error) {
	empty := runner.EventPage{Events: []runner.Event{}, Cursor: after}
	if after > core.MaxSafeInteger || limit > 100 {
		return empty, errors.New("invalid Task event page")
	}
	if limit == 0 {
		limit = 100
	}
	record, err := s.Tasks.Get(ctx, owner, task)
	if err != nil {
		return empty, err
	}
	if record == nil {
		return empty, errors.New("Task missing")
	}
	if record.Launch == nil && record.PreviousRunID != "" {
		record, err = s.Tasks.Run(ctx, owner, task, record.PreviousRunID)
		if err != nil {
			return empty, err
		}
	}
	if record == nil || record.Launch == nil {
		return empty, nil
	}
	launch := record.Launch
	if launch.RuntimeID == s.options.RuntimeID {
		return s.RunnerJournal.Events(ctx, launch.ControlID, task, launch.RunID, after, limit)
	}
	peer, remoteErr := s.Machines.Peer(launch.RuntimeID)
	var page runner.EventPage
	if remoteErr == nil {
		remoteErr = s.remoteCall(ctx, peer, binding(*launch), "task.events", "events:"+storage.ID(), map[string]uint64{"after": after, "limit": limit}, &page)
	}
	if remoteErr == nil {
		cursor := after
		launches := map[string]runner.Launch{launch.RunID: *launch}
		if len(page.Events) > int(limit) {
			return empty, errors.New("Runner transcript page exceeds limit")
		}
		for _, item := range page.Events {
			eventLaunch, known := launches[item.Event.TurnID]
			if !known {
				eventRun, err := s.Tasks.Run(ctx, owner, task, item.Event.TurnID)
				if err != nil {
					return empty, err
				}
				if eventRun == nil || eventRun.Launch == nil {
					return empty, errors.New("Runner transcript run missing")
				}
				eventLaunch = *eventRun.Launch
				launches[item.Event.TurnID] = eventLaunch
			}
			if item.Sequence != cursor+1 || item.Event.Generation != eventLaunch.Generation || eventLaunch.RuntimeID != launch.RuntimeID {
				return empty, errors.New("Runner transcript binding/order mismatch")
			}
			raw, err := json.Marshal(item.Event)
			if err != nil {
				return empty, err
			}
			var previous string
			err = s.Tasks.DB().QueryRowContext(ctx, "SELECT event FROM task_event_cache WHERE task_id=? AND sequence=?", task, item.Sequence).Scan(&previous)
			if err == nil && previous != string(raw) {
				return empty, errors.New("immutable Runner event changed")
			}
			if _, err = s.Tasks.DB().ExecContext(ctx, "INSERT INTO task_event_cache(task_id,sequence,event)VALUES(?,?,?)ON CONFLICT(task_id,sequence)DO NOTHING", task, item.Sequence, string(raw)); err != nil {
				return empty, err
			}
			cursor = item.Sequence
		}
		if page.Cursor != cursor {
			return empty, errors.New("Runner transcript cursor differs")
		}
		return page, nil
	}
	page = empty
	page.Unavailable = true
	rows, err := s.Tasks.DB().QueryContext(ctx, "SELECT sequence,event FROM task_event_cache WHERE task_id=? AND sequence>? ORDER BY sequence LIMIT ?", task, after, limit+1)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		if uint64(len(page.Events)) == limit {
			page.HasMore = true
			break
		}
		var item runner.Event
		var raw []byte
		if err = rows.Scan(&item.Sequence, &raw); err != nil {
			return page, err
		}
		if item.Sequence != page.Cursor+1 {
			page.HasMore = true
			break
		}
		if err = core.DecodeJSON(raw, &item.Event); err != nil {
			return page, err
		}
		page.Events = append(page.Events, item)
		page.Cursor = item.Sequence
	}
	return page, rows.Err()
}
