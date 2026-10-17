package consumer_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jjyr/pot/pkg/composition"
	"github.com/jjyr/pot/pkg/execution"
	"github.com/jjyr/pot/pkg/executor"
)

func TestExplicitCompositionWorker(t *testing.T) {
	worker := os.Getenv("POT_PUBLIC_API_WORKER")
	if worker == "" {
		t.Fatal("missing standalone composition worker")
	}
	var output strings.Builder
	runner := composition.ProcessRunner{Command: []string{worker}}
	err := runner.Run(context.Background(), `if (typeof process !== "undefined" || typeof require !== "undefined") throw new Error("ambient authority"); text(await tools.echo({message:"external"}));`, map[string]composition.Tool{
		"echo": func(_ context.Context, args json.RawMessage) (any, error) {
			var value map[string]any
			err := json.Unmarshal(args, &value)
			return value, err
		},
	}, func(value string) error { output.WriteString(value); return nil })
	if err != nil || output.String() != "{\"message\":\"external\"}\n" {
		t.Fatalf("worker: %q %v", output.String(), err)
	}
	// The host test executable has no hidden worker dispatch in its main.
	if err := (composition.ProcessRunner{}).Run(context.Background(), `text("unused");`, nil, nil); !errors.Is(err, composition.ErrWorkerCommandRequired) {
		t.Fatalf("implicit worker accepted: %v", err)
	}
}

func TestExecutionCapacityAndCancellation(t *testing.T) {
	manager, err := execution.New("external-runtime", "external-boot", execution.Limits{MaxActive: 1, MaxQueued: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Stop)
	started := make(chan struct{})
	settled := make(chan execution.Result, 1)
	go func() {
		_, result := manager.Execute(context.Background(), execution.Identity{Owner: "external", RequestID: "cancel", Generation: 1, Role: "assistant"}, func(ctx context.Context) execution.Result {
			close(started)
			<-ctx.Done()
			return execution.Result{Phase: execution.Cancelled, Err: ctx.Err()}
		})
		settled <- result
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("invocation did not start")
	}
	active := manager.Snapshot()
	if len(active) != 1 || active[0].Phase != execution.Running {
		t.Fatalf("active snapshot: %+v", active)
	}
	if !manager.Cancel(active[0].ID) {
		t.Fatal("cancel did not find execution")
	}
	select {
	case result := <-settled:
		if result.Phase != execution.Cancelled || !errors.Is(result.Err, context.Canceled) {
			t.Fatalf("cancel result: %+v", result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not settle")
	}
	_, result := manager.Execute(context.Background(), execution.Identity{Owner: "external", RequestID: "after-cancel", Generation: 1, Role: "assistant"}, func(context.Context) execution.Result { return execution.Result{Phase: execution.Succeeded} })
	if result.Phase != execution.Succeeded {
		t.Fatalf("capacity not released: %+v", result)
	}
	manager.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := manager.Drain(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestExecutorLifecycleFromExternalModule(t *testing.T) {
	directory := t.TempDir()
	engine := executor.New(executor.Options{Home: directory})
	t.Cleanup(func() {
		if err := engine.Close(); err != nil {
			t.Error(err)
		}
	})
	policy := executor.DefaultShellSettings()
	policy.ShellSnapshot = false
	policy.AllowLoginShell = false
	request := executor.Request{Mode: "host", ID: "external", CWD: directory, Policy: policy, Operation: "exec_command", Args: map[string]any{"cmd": "printf external", "yield_time_ms": 500}}
	result, err := engine.Execute(context.Background(), request, nil)
	if err != nil || result["stdout"] != "external" || result["exit_code"] != 0 {
		t.Fatalf("command: %v %v", result, err)
	}
	request.Args = map[string]any{"cmd": "sleep 60", "yield_time_ms": 1000}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := engine.Execute(ctx, request, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancel: %v", err)
	}
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	inventory, err := engine.Inspect()
	if err != nil || inventory["active"] != false {
		t.Fatalf("cleanup: %v %v", inventory, err)
	}
	if _, err := engine.Execute(context.Background(), request, nil); err == nil {
		t.Fatal("closed executor accepted work")
	}
}
