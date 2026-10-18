package composition

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCompositionWorker(t *testing.T) {
	if len(os.Args) > 1 && os.Args[len(os.Args)-1] == WorkerArgument {
		if ServeWorker(context.Background(), os.Stdin, os.Stdout) != nil {
			os.Exit(2)
		}
		os.Exit(0)
	}
}
func subprocessRunner(t *testing.T) ProcessRunner {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return ProcessRunner{Command: []string{path, "-test.run=^TestCompositionWorker$", "--", WorkerArgument}}
}

func TestProcessRequiresExplicitWorker(t *testing.T) {
	for _, command := range [][]string{nil, {""}} {
		err := (ProcessRunner{Command: command}).Run(context.Background(), `text("hello");`, nil, nil)
		if !errors.Is(err, ErrWorkerCommandRequired) {
			t.Fatalf("command %q: got %v; want ErrWorkerCommandRequired", command, err)
		}
	}
}

func TestProcessRPCAndCapabilityBoundary(t *testing.T) {
	r := subprocessRunner(t)
	var output strings.Builder
	yields := 0
	tools := map[string]Tool{"echo": func(ctx context.Context, args json.RawMessage) (any, error) {
		var v map[string]any
		if err := json.Unmarshal(args, &v); err != nil {
			return nil, err
		}
		return v, nil
	}}
	err := r.Run(context.Background(), `if (typeof process !== "undefined" || typeof require !== "undefined") throw new Error("ambient authority"); text("before");yield_control();const values=await Promise.all([tools.echo({id:1}),tools.echo({id:2})]);text(values);`, tools, func(event Event) error {
		if event.Type == "yield" {
			if output.String() != "before\n" {
				return errors.New("yield lost its preceding output")
			}
			yields++
		}
		output.WriteString(event.Text)
		return nil
	})
	if err != nil || output.String() != "before\n[{\"id\":1},{\"id\":2}]\n" || yields != 1 {
		t.Fatal(err, output.String(), yields)
	}
}
func TestProcessExitWaitsForChildCleanup(t *testing.T) {
	r := subprocessRunner(t)
	var settled atomic.Bool
	entered := make(chan struct{})
	tools := map[string]Tool{"pending": func(ctx context.Context, _ json.RawMessage) (any, error) {
		close(entered)
		<-ctx.Done()
		time.Sleep(30 * time.Millisecond)
		settled.Store(true)
		return nil, ctx.Err()
	}, "ready": func(ctx context.Context, _ json.RawMessage) (any, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-entered:
			return true, nil
		}
	}}
	if err := r.Run(context.Background(), `tools.pending({});await tools.ready({});text("done");`, tools, nil); err != nil {
		t.Fatal(err)
	}
	if !settled.Load() {
		t.Fatal("returned before child cleanup")
	}
}
func TestProcessFatalCleanupCannotBeCaught(t *testing.T) {
	r := subprocessRunner(t)
	err := r.Run(context.Background(), `try {await tools.fail({})} catch (_) {} text("ignored");`, map[string]Tool{"fail": func(context.Context, json.RawMessage) (any, error) {
		return nil, &UnconfirmedCleanupError{Cause: errors.New("test fence")}
	}}, nil)
	var uncertain *UnconfirmedCleanupError
	if !errors.As(err, &uncertain) {
		t.Fatal(err)
	}
}
func TestProcessInfiniteScriptIsInterrupted(t *testing.T) {
	r := subprocessRunner(t)
	r.Limits.JavaScriptTime = 20 * time.Millisecond
	if err := r.Run(context.Background(), `for (;;) {}`, nil, nil); err == nil || !strings.Contains(err.Error(), "time limit") {
		t.Fatal(err)
	}
}
