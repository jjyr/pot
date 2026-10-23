package control

import (
	"context"
	"errors"
	"github.com/jjyr/pot/internal/tasks"
	"github.com/jjyr/pot/pkg/core"
)

func (s *Service) TaskEvents(ctx context.Context, owner, task string, after, limit uint64) (tasks.EventPage, error) {
	empty := tasks.EventPage{Events: []tasks.Event{}, Cursor: after}
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
		return s.RunJournal.Events(ctx, launch.ControlID, task, launch.RunID, after, limit)
	}
	page := empty
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
		var item tasks.Event
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
