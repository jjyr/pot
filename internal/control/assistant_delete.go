package control

import (
	"context"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

// DeleteAssistant retires an idle identity. Durable history remains private and
// inaccessible through conversation APIs; its ID is never reused.
func (s *Service) DeleteAssistant(ctx context.Context, id string, revision uint64) error {
	if !core.Identifier(id) || revision == 0 || revision > core.MaxSafeInteger {
		return protocol.APIError{Code: "assistant_changed"}
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return protocol.APIError{Code: "assistant_unavailable"}
	}
	commit := func() error {
		return s.Tasks.RetireOwner(ctx, id, func() error { return s.Registry.Delete(ctx, id, revision) })
	}
	c := s.conversations[id]
	var err error
	if c != nil {
		err = c.Retire(commit)
	} else {
		err = commit()
	}
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if c != nil {
		_ = c.Close()
	}
	s.Changes.Notify("assistants")
	s.Changes.Notify("schedules")
	s.wakeTasks()
	return nil
}
