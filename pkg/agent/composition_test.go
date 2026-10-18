package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jjyr/pot/pkg/composition"
	"github.com/jjyr/pot/pkg/core"
	m "github.com/jjyr/pot/pkg/modelprotocol"
)

func TestCompositionTextWaitsForCompletion(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	started, release := make(chan struct{}), make(chan struct{})
	manager := &compositionManager{ctx: ctx, composer: composition.Runner{}, jobs: map[string]*compositionJob{}, tools: []m.ToolDefinition{{Name: "block"}}, emit: func(core.Update) error { return nil }, dispatch: func(ctx context.Context, _ core.ExecutionContext, _ m.ToolCall, _ func(core.Update) error) (json.RawMessage, error) {
		close(started)
		select {
		case <-release:
			return json.RawMessage(`"after"`), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}}
	defer manager.close()
	finished := make(chan json.RawMessage, 1)
	errors := make(chan error, 1)
	go func() {
		raw, err := manager.call("exec", json.RawMessage(`{"code":"text('before');text(await tools.block({}));"}`), "test")
		finished <- raw
		errors <- err
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case raw := <-finished:
		cancel()
		<-errors
		t.Fatal("text yielded before the script completed", string(raw))
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	select {
	case raw := <-finished:
		if err := <-errors; err != nil {
			t.Fatal(err)
		}
		var result map[string]any
		if err := json.Unmarshal(raw, &result); err != nil || result["completed"] != true || result["output"] != "before\nafter\n" || result["exec_id"] != nil {
			t.Fatal("short script requires an unnecessary wait", string(raw), err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func TestCompositionExplicitYieldAndTimeoutDoNotReplayOutput(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "timeout", true: "yield_control"}[explicit], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			release := make(chan struct{})
			manager := &compositionManager{ctx: ctx, composer: composition.Runner{}, jobs: map[string]*compositionJob{}, tools: []m.ToolDefinition{{Name: "block"}}, emit: func(core.Update) error { return nil }, dispatch: func(ctx context.Context, _ core.ExecutionContext, _ m.ToolCall, _ func(core.Update) error) (json.RawMessage, error) {
				select {
				case <-release:
					return json.RawMessage(`"after"`), nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}}
			defer manager.close()
			args := map[string]any{"code": "text('before');yield_control();text(await tools.block({}));"}
			if !explicit {
				args["code"] = "text('before');text(await tools.block({}));"
				args["yield_time_ms"] = 50
			}
			encoded, _ := json.Marshal(args)
			raw, err := manager.call("exec", encoded, "test")
			if err != nil {
				t.Fatal(err)
			}
			var result map[string]any
			if err := json.Unmarshal(raw, &result); err != nil || result["completed"] != false || result["output"] != "before\n" {
				t.Fatal(string(raw), err)
			}
			id, ok := result["exec_id"].(string)
			if !ok {
				t.Fatal(string(raw))
			}
			close(release)
			encoded, _ = json.Marshal(map[string]any{"exec_id": id})
			raw, err = manager.call("wait", encoded, "test")
			if err != nil {
				t.Fatal(err)
			}
			result = nil
			if err := json.Unmarshal(raw, &result); err != nil || result["completed"] != true || result["output"] != "after\n" {
				t.Fatal(string(raw), err)
			}
			if _, err := manager.call("wait", encoded, "test"); err == nil {
				t.Fatal("completed output was replayable")
			}
		})
	}
}

func TestCompositionSequentialRunsReleaseCapacityIncludingFailures(t *testing.T) {
	manager := &compositionManager{ctx: context.Background(), composer: composition.Runner{}, jobs: map[string]*compositionJob{}}
	defer manager.close()
	for i := 0; i < 100; i++ {
		code := `void 0`
		if i%2 == 0 {
			code = `throw new Error("expected")`
		}
		args, _ := json.Marshal(map[string]any{"code": code, "yield_time_ms": 1000})
		result, err := manager.call("exec", args, "test")
		if err != nil {
			t.Fatal(i, err)
		}
		var value map[string]any
		if err := json.Unmarshal(result, &value); err != nil {
			t.Fatal(err)
		}
		if value["completed"] != true || len(manager.jobs) != 0 {
			t.Fatal("completed run retained turn capacity", i, string(result), len(manager.jobs))
		}
		if (value["isError"] == true) != (i%2 == 0) {
			t.Fatal(string(result))
		}
	}
}

func TestCompositionActiveRunsKeepLimitAndReleaseAfterWait(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	release := make(chan struct{})
	manager := &compositionManager{ctx: ctx, composer: composition.Runner{}, jobs: map[string]*compositionJob{}, tools: []m.ToolDefinition{{Name: "block"}}, emit: func(core.Update) error { return nil }, dispatch: func(ctx context.Context, _ core.ExecutionContext, _ m.ToolCall, _ func(core.Update) error) (json.RawMessage, error) {
		select {
		case <-release:
			return json.RawMessage(`null`), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}}
	defer manager.close()
	ids := []string{}
	for i := 0; i < 4; i++ {
		raw, err := manager.call("exec", json.RawMessage(`{"code":"await tools.block({})","yield_time_ms":1}`), "test")
		if err != nil {
			t.Fatal(err)
		}
		var value map[string]any
		json.Unmarshal(raw, &value)
		id, ok := value["exec_id"].(string)
		if !ok {
			t.Fatal(string(raw))
		}
		ids = append(ids, id)
	}
	if _, err := manager.call("exec", json.RawMessage(`{"code":"text('extra')"}`), "test"); err == nil || !strings.Contains(err.Error(), "active run limit") {
		t.Fatal(err)
	}
	close(release)
	for _, id := range ids {
		select {
		case <-manager.jobs[id].done:
		case <-time.After(time.Second):
			t.Fatal("run did not complete")
		}
		args, _ := json.Marshal(map[string]any{"exec_id": id, "yield_time_ms": 1000})
		if _, err := manager.call("wait", args, "test"); err != nil {
			t.Fatal(err)
		}
		if _, err := manager.call("wait", args, "test"); err == nil {
			t.Fatal("completed result replayed")
		}
	}
	if _, err := manager.call("exec", json.RawMessage(`{"code":"text('next')","yield_time_ms":1000}`), "test"); err != nil {
		t.Fatal(err)
	}
}
