package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/jjyr/pot/internal/agent"
)

type runFeedback struct {
	journal            *Journal
	control, task, run string
	upstream           agent.Feedback
}

func (f *runFeedback) current(ctx context.Context) (*Run, error) {
	run, err := f.journal.Inspect(ctx, f.control, f.task, f.run)
	if err != nil {
		return nil, err
	}
	if run == nil || run.Phase != Running || run.BootID != f.journal.BootID || run.StopRequested != "" {
		return nil, fmt.Errorf("task Agent run is fenced")
	}
	if f.upstream == nil {
		return nil, fmt.Errorf("run-bound Control feedback unavailable")
	}
	return run, nil
}
func (f *runFeedback) LoadContext(ctx context.Context) (json.RawMessage, error) {
	run, err := f.current(ctx)
	if err != nil {
		return nil, err
	}
	return run.Context, nil
}
func (f *runFeedback) SaveContext(ctx context.Context, raw json.RawMessage) error {
	return f.journal.SaveContext(ctx, f.control, f.task, f.run, raw)
}
func (f *runFeedback) RecordTurn(ctx context.Context, id string) error {
	if id != f.run {
		return fmt.Errorf("task turn identity mismatch")
	}
	if _, err := f.current(ctx); err != nil {
		return err
	}
	return f.upstream.RecordTurn(ctx, id)
}
func (f *runFeedback) InputStreaming(ctx context.Context, id string) error {
	if id != f.run {
		return fmt.Errorf("task turn identity mismatch")
	}
	if _, err := f.current(ctx); err != nil {
		return err
	}
	return f.upstream.InputStreaming(ctx, id)
}
func (f *runFeedback) Ready(ctx context.Context) error {
	if _, err := f.current(ctx); err != nil {
		return err
	}
	return f.upstream.Ready(ctx)
}
func (f *runFeedback) Steering(ctx context.Context, final bool) ([]agent.SteeringInput, error) {
	if _, err := f.current(ctx); err != nil {
		return nil, err
	}
	return f.upstream.Steering(ctx, final)
}
func (f *runFeedback) AcknowledgeSteering(ctx context.Context, ids []string) error {
	if _, err := f.current(ctx); err != nil {
		return err
	}
	return f.upstream.AcknowledgeSteering(ctx, ids)
}
func (f *runFeedback) Observe(ctx context.Context, event agent.Observation) error {
	if _, err := f.current(ctx); err != nil {
		return err
	}
	return f.upstream.Observe(ctx, event)
}
func (f *runFeedback) Settle(ctx context.Context) error {
	if f.upstream == nil {
		return fmt.Errorf("Control settlement unavailable")
	}
	if err := f.upstream.Settle(ctx); err != nil {
		return err
	}
	return f.journal.Settle(ctx, f.control, f.task, f.run)
}
