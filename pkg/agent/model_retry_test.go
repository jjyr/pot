package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

func TestModelRetryBackoffAndLimit(t *testing.T) {
	for _, recover := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			a, f, s, thread, execution := setup(t)
			failure := &core.TransientModelError{Cause: io.ErrUnexpectedEOF}
			var ids []string
			var starts []time.Time
			s.model = func(request core.ModelRequest, index int) (string, error) {
				if slices.Contains(ids, request.ID) {
					t.Fatal("retry reused a metered request identity")
				}
				ids = append(ids, request.ID)
				starts = append(starts, time.Now())
				if recover && index == 3 {
					return response("recovered"), nil
				}
				return "", failure
			}
			start := time.Now()
			err := a.Execute(context.Background(), execution, thread, core.TurnRequest{RequestID: "turn", Text: "hello"}, false, func(core.AgentEvent) error { return nil })
			if recover && err != nil || !recover && !errors.Is(err, failure) {
				t.Fatal(err)
			}
			if s.calls != 4 || time.Since(start) != 14*time.Second {
				t.Fatal("incorrect retry limit/backoff", s.calls, time.Since(start))
			}
			for i, delay := range []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second} {
				if starts[i+1].Sub(starts[i]) != delay {
					t.Fatal("incorrect retry interval", i, starts[i+1].Sub(starts[i]))
				}
			}
			var state Checkpoint
			if err := json.Unmarshal(f.body, &state); err != nil || state.ModelRequestID != nil {
				t.Fatal("retry left a model fence", state, err)
			}
		})
	}
}

func TestRejectedTemporaryProviderResponseRetries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a, _, s, thread, execution := setup(t)
		s.model = func(_ core.ModelRequest, index int) (string, error) {
			if index == 0 {
				return "", &core.ModelRequestError{Disposition: "rejected", Cause: &core.TransientModelError{Cause: errors.New("HTTP 503")}}
			}
			return response("recovered"), nil
		}
		if err := a.Execute(context.Background(), execution, thread, core.TurnRequest{RequestID: "turn", Text: "hello"}, false, func(core.AgentEvent) error { return nil }); err != nil || s.calls != 2 {
			t.Fatal("temporary HTTP rejection was not retried", err, s.calls)
		}
	})
}

func TestModelRetryStopsDuringBackoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a, _, s, thread, execution := setup(t)
		s.model = func(core.ModelRequest, int) (string, error) {
			return "", &core.TransientModelError{Cause: io.ErrUnexpectedEOF}
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, 1)
		go func() {
			done <- a.Execute(ctx, execution, thread, core.TurnRequest{RequestID: "turn", Text: "hello"}, false, func(core.AgentEvent) error { return nil })
		}()
		synctest.Wait()
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) || s.calls != 1 {
			t.Fatal("stop sent another model request", err, s.calls)
		}
	})
}

func TestModelRetryDoesNotHidePermanentOrPersistenceErrors(t *testing.T) {
	for _, failure := range []error{
		errors.New("invalid model response"),
		context.Canceled,
		&protocol.APIError{Code: "pot_token_limit", Message: "allowance unavailable"},
		&core.ModelRequestError{Disposition: "unsent", Cause: errors.New("invalid configuration")},
		&core.ModelRequestError{Disposition: "rejected", Cause: errors.New("HTTP 401")},
	} {
		a, _, s, thread, execution := setup(t)
		s.model = func(core.ModelRequest, int) (string, error) { return "", failure }
		if err := a.Execute(context.Background(), execution, thread, core.TurnRequest{RequestID: "turn", Text: "hello"}, false, func(core.AgentEvent) error { return nil }); !errors.Is(err, failure) || s.calls != 1 {
			t.Fatal("retried a permanent failure", failure, err, s.calls)
		}
	}
	a, f, s, thread, execution := setup(t)
	s.model = func(core.ModelRequest, int) (string, error) {
		f.saveErr = errors.New("disk full")
		return "", &core.TransientModelError{Cause: io.ErrUnexpectedEOF}
	}
	if err := a.Execute(context.Background(), execution, thread, core.TurnRequest{RequestID: "turn", Text: "hello"}, false, func(core.AgentEvent) error { return nil }); !errors.Is(err, f.saveErr) || s.calls != 1 {
		t.Fatal("retried without a durable checkpoint", err, s.calls)
	}
}

type modelServiceOverride struct {
	Services
	model func(context.Context, core.ModelRequest) (io.ReadCloser, error)
}

func (s modelServiceOverride) Model(ctx context.Context, request core.ModelRequest) (io.ReadCloser, error) {
	return s.model(ctx, request)
}

func TestModelAttemptTimeoutRetriesWithinLiveTurn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a, _, s, thread, execution := setup(t)
		s.model = noTools
		calls := 0
		a.Services = modelServiceOverride{Services: s, model: func(ctx context.Context, request core.ModelRequest) (io.ReadCloser, error) {
			calls++
			if calls == 1 {
				<-ctx.Done()
				return nil, ctx.Err()
			}
			return s.Model(ctx, request)
		}}
		start := time.Now()
		if err := a.Execute(context.Background(), execution, thread, core.TurnRequest{RequestID: "turn", Text: "hello"}, false, func(core.AgentEvent) error { return nil }); err != nil {
			t.Fatal(err)
		}
		if calls != 2 || time.Since(start) != 602*time.Second {
			t.Fatal("attempt timeout was not retried", calls, time.Since(start))
		}
	})
}

func TestModelTimeoutExhaustionFailsButTurnDeadlineCancels(t *testing.T) {
	for _, turnDeadline := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			a, _, s, thread, execution := setup(t)
			calls := 0
			a.Services = modelServiceOverride{Services: s, model: func(ctx context.Context, request core.ModelRequest) (io.ReadCloser, error) {
				calls++
				<-ctx.Done()
				return nil, ctx.Err()
			}}
			ctx := context.Background()
			if turnDeadline {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, time.Second)
				defer cancel()
			}
			var outcome *core.Outcome
			err := a.Execute(ctx, execution, thread, core.TurnRequest{RequestID: "turn", Text: "hello"}, false, func(event core.AgentEvent) error {
				if event.Update.Type == "turn_finished" {
					outcome = event.Update.Outcome
				}
				return nil
			})
			if turnDeadline {
				if !errors.Is(err, context.DeadlineExceeded) || calls != 1 || outcome == nil || outcome.Kind != "cancelled" {
					t.Fatal("turn deadline did not cancel promptly", err, calls, outcome)
				}
			} else if err == nil || calls != 4 || outcome == nil || outcome.Kind != "failed" {
				t.Fatal("exhausted model timeouts were not a terminal failure", err, calls, outcome)
			}
		})
	}
}
