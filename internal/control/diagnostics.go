package control

import (
	"context"
	"errors"
)

func (s *Service) TaskDiagnostics(ctx context.Context, owner, task string, after uint64) (map[string]any, error) {
	record, err := s.Tasks.Get(ctx, owner, task)
	if err != nil {
		return nil, err
	}
	if record == nil {
		return nil, errors.New("Task unavailable")
	}
	page, err := s.TaskEvents(ctx, owner, task, after, 100)
	if err != nil {
		return nil, err
	}
	entries := []map[string]any{}
	for _, entry := range page.Events {
		u := entry.Event.Update
		entries = append(entries, map[string]any{"sequence": entry.Sequence, "turn_id": entry.Event.TurnID, "generation": entry.Event.Generation, "kind": u.Type, "id": u.ID, "tool": u.Name, "failed": u.Failed})
	}
	return map[string]any{"owner": task, "entries": entries, "cursor": page.Cursor, "has_more": page.HasMore, "unavailable": page.Unavailable, "control": map[string]any{"run_id": record.RunID, "paused": record.Info.Paused}}, nil
}
