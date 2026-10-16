package tasks

import (
	"context"
	"database/sql"
	"fmt"
)

// Control applies only to the original accepted run. It never changes the fixed
// destination or resumes an allowance/interrupted run by creating another run.
func (s *Service) Control(ctx context.Context, assistant, task, run, action string) error {
	if action != "pause" && action != "resume" {
		return fmt.Errorf("invalid Task control")
	}
	return s.change(ctx, assistant, task, func(_ *sql.Tx, r *Record) error {
		if r.RunID != run || r.Launch == nil || r.Info.Phase.Scope() != "active" || r.StopMode != "" || r.Finalizing {
			return fmt.Errorf("original Task run ended or is stopping")
		}
		r.Info.Paused = action == "pause"
		return nil
	})
}
func (s *Service) ReadyGate(ctx context.Context, assistant, task, run string) error {
	changes := s.Subscribe()
	defer changes.Close()
	for {
		r, err := s.Get(ctx, assistant, task)
		if err != nil {
			return err
		}
		if r == nil || r.RunID != run || r.Launch == nil || r.Info.Phase.Scope() != "active" || r.StopMode != "" {
			return fmt.Errorf("Task run ended or is stopping")
		}
		if !r.Info.Paused {
			return ctx.Err()
		}
		if _, err = changes.Next(ctx); err != nil {
			return err
		}
	}
}
