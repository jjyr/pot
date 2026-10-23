package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/jjyr/pot/pkg/core"
)

func TestActiveModelStreamOutlivesTotalTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a, f, s, thread, execution := setup(t)
		calls := 0
		a.Services = modelServiceOverride{Services: s, model: func(ctx context.Context, request core.ModelRequest) (io.ReadCloser, error) {
			calls++
			// Opening may exceed the old 180-second cap, too.
			time.Sleep(599 * time.Second)
			r, w := io.Pipe()
			go func() {
				defer w.Close()
				for _, part := range []string{
					": provider heartbeat\n\n",
					"data: {\"choices\":[{\"delta\":{\"content\":\"done\"}}]}\n\n",
					"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n",
					"data: [DONE]\n\n",
				} {
					time.Sleep(599 * time.Second)
					if _, err := io.WriteString(w, part); err != nil {
						return
					}
				}
			}()
			return r, nil
		}}
		start := time.Now()
		if err := a.Execute(context.Background(), execution, thread, core.TurnRequest{RequestID: "turn", Text: "hello"}, false, func(core.AgentEvent) error { return nil }); err != nil {
			t.Fatal(err)
		}
		var state Checkpoint
		if err := json.Unmarshal(f.body, &state); err != nil {
			t.Fatal(err)
		}
		if calls != 1 || time.Since(start) != 2995*time.Second || state.Messages[len(state.Messages)-1].Text != "done" {
			t.Fatal("active response was interrupted/retried", calls, time.Since(start), state)
		}
	})
}

func TestModelIdleTimeoutRenewsAfterDataAndRetries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a, _, s, thread, execution := setup(t)
		s.model = noTools
		calls := 0
		a.Services = modelServiceOverride{Services: s, model: func(ctx context.Context, request core.ModelRequest) (io.ReadCloser, error) {
			calls++
			if calls > 1 {
				return s.Model(ctx, request)
			}
			r, w := io.Pipe()
			go func() {
				time.Sleep(500 * time.Second)
				_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
				<-ctx.Done()
				// A real HTTP transport may expose a socket error rather than
				// context cancellation; the inactivity cause must win.
				w.CloseWithError(net.ErrClosed)
			}()
			return r, nil
		}}
		start := time.Now()
		if err := a.Execute(context.Background(), execution, thread, core.TurnRequest{RequestID: "turn", Text: "hello"}, false, func(core.AgentEvent) error { return nil }); err != nil {
			t.Fatal(err)
		}
		if calls != 2 || time.Since(start) != 1102*time.Second {
			t.Fatal("idle interval did not renew after data", calls, time.Since(start))
		}
	})
}

func TestModelIdleTimeoutExhaustionReportsCause(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a, _, s, thread, execution := setup(t)
		a.Services = modelServiceOverride{Services: s, model: func(ctx context.Context, _ core.ModelRequest) (io.ReadCloser, error) {
			r, w := io.Pipe()
			go func() {
				<-ctx.Done()
				w.CloseWithError(net.ErrClosed)
			}()
			return r, nil
		}}
		start := time.Now()
		err := a.Execute(context.Background(), execution, thread, core.TurnRequest{RequestID: "turn", Text: "hello"}, false, func(core.AgentEvent) error { return nil })
		if !errors.Is(err, errModelIdleTimeout) || errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "closed network") || time.Since(start) != 2414*time.Second {
			t.Fatal("idle failure lost its diagnostic or retry cap", err, time.Since(start))
		}
	})
}

func TestTurnCancellationClosesBlockedModelReadWithoutRetry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a, _, s, thread, execution := setup(t)
		calls := 0
		var body *io.PipeReader
		a.Services = modelServiceOverride{Services: s, model: func(context.Context, core.ModelRequest) (io.ReadCloser, error) {
			calls++
			var writer *io.PipeWriter
			body, writer = io.Pipe()
			t.Cleanup(func() { writer.Close() })
			return body, nil
		}}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		start := time.Now()
		err := a.Execute(ctx, execution, thread, core.TurnRequest{RequestID: "turn", Text: "hello"}, false, func(core.AgentEvent) error { return nil })
		if !errors.Is(err, context.DeadlineExceeded) || calls != 1 || time.Since(start) != time.Second {
			t.Fatal("turn cancellation did not interrupt the read", err, calls, time.Since(start))
		}
		if _, err := body.Read(make([]byte, 1)); !errors.Is(err, io.ErrClosedPipe) {
			t.Fatal("model body remained open after turn deadline", err)
		}
	})
}
