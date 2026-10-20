package control

import (
	"context"
	"database/sql"
	"github.com/jjyr/pot/internal/assistant"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/storage"
	"time"
)

func (s *Service) idleCompactPolicy(_ context.Context, thread core.Thread) (assistant.IdleCompactionPolicy, error) {
	s.Profiles.mu.Lock()
	defer s.Profiles.mu.Unlock()
	for _, profile := range s.Profiles.state.Profiles {
		if profile.ID == thread.Config.SourceProfileID {
			minutes := uint64(15)
			source := "default"
			if settings := profile.ModelSettings; settings != nil {
				if settings.IdleCompactMinutes != nil {
					minutes = *settings.IdleCompactMinutes
					source = "connection"
				}
				if value, ok := settings.IdleCompactModels[thread.Config.Model]; ok {
					minutes = value
					source = "model"
				}
			}
			return assistant.IdleCompactionPolicy{Delay: time.Duration(minutes) * time.Minute, Source: source}, nil
		}
	}
	return assistant.IdleCompactionPolicy{}, nil // A removed connection cannot schedule new background work.
}

func (s *Service) compactGate(owner string) func(context.Context, func() error) error {
	return func(ctx context.Context, start func() error) error {
		// BEGIN IMMEDIATE shares the terminal-task/mailbox writer boundary, including
		// a completed outcome whose notification has not yet reached the inbox.
		return storage.Transaction(ctx, s.Tasks.DB(), func(tx *sql.Tx) error {
			var pending bool
			err := tx.QueryRowContext(ctx, `SELECT EXISTS(
    SELECT 1 FROM tasks t WHERE assistant_id=? AND json_extract(t.body,'$.outcome') IS NOT NULL
    AND COALESCE(json_extract(t.body,'$.outcome.suppression_reason'),'')=''
    AND NOT EXISTS(SELECT 1 FROM agent_messages m WHERE m.recipient=? AND m.direction='in' AND m.state='processed'
      AND json_extract(m.envelope,'$.kind')='task_result'
      AND json_extract(m.envelope,'$.result.task_id')=t.id
      AND json_extract(m.envelope,'$.result.run_id')=json_extract(t.body,'$.run_id'))
   ) OR EXISTS(SELECT 1 FROM agent_messages WHERE recipient=? AND direction='in' AND state IN ('queued','processing') AND json_extract(envelope,'$.kind')='task_result')`, owner, owner, owner).Scan(&pending)
			if err != nil {
				return err
			}
			if pending {
				return assistant.ErrCompactDeferred
			}
			return start()
		})
	}
}
