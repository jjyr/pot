package composition

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func runOutput(ctx context.Context, runner Runner, code string, tools map[string]Tool) (string, error) {
	var output strings.Builder
	err := runner.Run(ctx, code, tools, func(event Event) error { _, err := output.WriteString(event.Text); return err })
	return output.String(), err
}

func TestParallelToolsKeepResultsPrivateAndJSONOnly(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var started atomic.Int32
	barrier := make(chan struct{})
	tools := map[string]Tool{"probe": func(ctx context.Context, args json.RawMessage) (any, error) {
		if started.Add(1) == 2 {
			close(barrier)
		}
		select {
		case <-barrier:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		var value map[string]int
		if err := json.Unmarshal(args, &value); err != nil {
			return nil, err
		}
		return map[string]any{"value": value["value"], "private": "secret-child-result"}, nil
	}}
	output, err := runOutput(ctx, Runner{}, `
const r = await Promise.all([tools.probe({value:2}), tools.probe({value:3})]);
text(r.reduce((sum, result) => sum + result.value, 0));
text(Object.getPrototypeOf(r[0]) === Object.prototype);
`, tools)
	if err != nil {
		t.Fatal(err)
	}
	if output != "5\ntrue\n" {
		t.Fatalf("output = %q", output)
	}
}

func TestStreamingOwnerAndToolCancellationOnCompletion(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started, release, cancelled := make(chan struct{}), make(chan struct{}), make(chan struct{})
	tools := make(map[string]Tool)
	tools["probe"] = func(ctx context.Context, _ json.RawMessage) (any, error) {
		close(started)
		select {
		case <-release:
			return "after", nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	tools["background"] = func(ctx context.Context, _ json.RawMessage) (any, error) {
		<-ctx.Done()
		close(cancelled)
		return nil, ctx.Err()
	}
	chunks := make(chan string, 3)
	finished := make(chan error, 1)
	go func() {
		finished <- (Runner{}).Run(ctx, `text('before'); tools.background(); text(await tools.probe());`, tools,
			func(event Event) error { chunks <- event.Text; return nil })
	}()
	select {
	case chunk := <-chunks:
		if chunk != "before\n" {
			t.Fatal(chunk)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	close(release)
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if chunk := <-chunks; chunk != "after\n" {
		t.Fatal(chunk)
	}
	select {
	case <-cancelled:
	case <-ctx.Done():
		t.Fatal("unawaited child not cancelled")
	}
}

func TestCapabilitiesAndErrors(t *testing.T) {
	output, err := runOutput(context.Background(), Runner{}, `
text([typeof process, typeof require, typeof fetch, typeof setTimeout, typeof console,
typeof tools.exec, typeof tools.wait]);
text(Object.isFrozen(tools));
try { await tools.failure({}); } catch(e) { text(e.message); }
text(typeof tools.toString);
try { await tools.failure({}); } catch(e) { text(typeof e.value); }
`, map[string]Tool{"failure": func(context.Context, json.RawMessage) (any, error) { return nil, errors.New("approval denied") }})
	if err != nil {
		t.Fatal(err)
	}
	if output != "[\"undefined\",\"undefined\",\"undefined\",\"undefined\",\"undefined\",\"undefined\",\"undefined\"]\ntrue\napproval denied\nundefined\nundefined\n" {
		t.Fatalf("output=%q", output)
	}
	for _, code := range []string{`throw new Error('observable failure')`, `await tools.unknown({})`, `const = broken`} {
		if _, err := runOutput(context.Background(), Runner{}, code, nil); err == nil {
			t.Fatalf("expected error for %s", code)
		}
	}
	for _, name := range []string{"exec", "wait", ""} {
		err := (Runner{}).Run(context.Background(), "text(1)", map[string]Tool{name: func(context.Context, json.RawMessage) (any, error) { return nil, nil }}, nil)
		if err == nil {
			t.Fatalf("accepted reserved name %q", name)
		}
	}
}

func TestToolCallsAndConcurrencyAreBounded(t *testing.T) {
	var calls atomic.Int32
	tool := func(context.Context, json.RawMessage) (any, error) { calls.Add(1); return nil, nil }
	_, err := runOutput(context.Background(), Runner{Limits: Limits{Calls: 2}}, `for(let i=0;i<3;i++) await tools.probe({});`, map[string]Tool{"probe": tool})
	if err == nil || !strings.Contains(err.Error(), "call limit") || calls.Load() != 2 {
		t.Fatalf("calls=%d err=%v", calls.Load(), err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var active, maximum atomic.Int32
	release := make(chan struct{})
	var ready sync.Once
	blocked := func(ctx context.Context, _ json.RawMessage) (any, error) {
		n := active.Add(1)
		defer active.Add(-1)
		for old := maximum.Load(); n > old; old = maximum.Load() {
			if maximum.CompareAndSwap(old, n) {
				break
			}
		}
		if n == 2 {
			ready.Do(func() { close(release) })
		}
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return nil, nil
	}
	output, err := runOutput(ctx, Runner{Limits: Limits{ConcurrentCalls: 2}}, `
const a = tools.probe(), b = tools.probe();
try { await tools.probe(); } catch(e) { text(e.message); }
await Promise.all([a,b]);
`, map[string]Tool{"probe": blocked})
	if err != nil || !strings.Contains(output, "concurrent tool limit") || maximum.Load() != 2 {
		t.Fatalf("output=%q max=%d err=%v", output, maximum.Load(), err)
	}
}

func TestCodeOutputAndJSONLimits(t *testing.T) {
	if err := (Runner{Limits: Limits{CodeBytes: 3}}).Run(context.Background(), "text(1)", nil, nil); !errors.Is(err, ErrCodeLimit) {
		t.Fatal(err)
	}
	output, err := runOutput(context.Background(), Runner{Limits: Limits{OutputBytes: 4}}, `try { text('abc'); text('x'); } catch(e) {}`, nil)
	if !errors.Is(err, ErrOutputLimit) || output != "abc\n" {
		t.Fatalf("output=%q err=%v", output, err)
	}
	tool := func(context.Context, json.RawMessage) (any, error) { return strings.Repeat("r", 20), nil }
	for _, code := range []string{`await tools.probe({long:'argument'})`, `await tools.probe({})`} {
		_, err := runOutput(context.Background(), Runner{Limits: Limits{JSONBytes: 8}}, code, map[string]Tool{"probe": tool})
		if err == nil || !strings.Contains(err.Error(), "JSON exceeds") {
			t.Fatalf("code=%s err=%v", code, err)
		}
	}
	_, err = runOutput(context.Background(), Runner{}, `const a = {}; a.a = a; await tools.probe(a)`, map[string]Tool{"probe": tool})
	if err == nil {
		t.Fatal("cyclic JSON accepted")
	}
}

func TestCancellationAndUninterruptedBudget(t *testing.T) {
	for _, code := range []string{
		`while(true) {}`,
		`await Promise.resolve(); while(true) {}`,
		`await tools.probe(); while(true) {}`,
		`throw { toString() { while(true){} } }`,
		`await tools.probe({toJSON(){throw {toString(){while(true){}}}}})`,
		`text({toJSON(){throw {toString(){while(true){}}}}})`,
	} {
		_, err := runOutput(context.Background(), Runner{Limits: Limits{JavaScriptTime: 20 * time.Millisecond}}, code, map[string]Tool{"probe": func(context.Context, json.RawMessage) (any, error) { return nil, nil }})
		if !errors.Is(err, ErrScriptTimeLimit) {
			t.Fatalf("code=%s err=%v", code, err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := runOutput(ctx, Runner{}, `while(true) {}`, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if _, err := runOutput(context.Background(), Runner{Limits: Limits{WallTime: 20 * time.Millisecond}}, `await new Promise(()=>{})`, nil); !errors.Is(err, ErrWallTimeLimit) {
		t.Fatal(err)
	}
	_, err := runOutput(context.Background(), Runner{Limits: Limits{StackDepth: 32}}, `function recur() { return recur() }; recur();`, nil)
	if err == nil {
		t.Fatal("unbounded recursion succeeded")
	}
}

func TestWaitingForToolsDoesNotConsumeJavaScriptBudget(t *testing.T) {
	output, err := runOutput(context.Background(), Runner{Limits: Limits{JavaScriptTime: 20 * time.Millisecond}},
		`text(await tools.probe())`, map[string]Tool{"probe": func(ctx context.Context, _ json.RawMessage) (any, error) {
			select {
			case <-time.After(80 * time.Millisecond):
				return "after wait", nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}})
	if err != nil || output != "after wait\n" {
		t.Fatalf("output=%q err=%v", output, err)
	}
}

func TestCancellationReachesChildTools(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	started, stopped := make(chan struct{}), make(chan struct{})
	tool := func(ctx context.Context, _ json.RawMessage) (any, error) {
		close(started)
		<-ctx.Done()
		close(stopped)
		return nil, ctx.Err()
	}
	finished := make(chan error, 1)
	go func() {
		_, err := runOutput(ctx, Runner{}, `await tools.probe()`, map[string]Tool{"probe": tool})
		finished <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("tool not started")
	}
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not stop")
	}
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("child not cancelled")
	}
}

func TestUnconfirmedCleanupAndEmitterFailureCannotBeCaught(t *testing.T) {
	cause := errors.New("process descendants remain")
	_, err := runOutput(context.Background(), Runner{}, `try { await tools.probe() } catch(e) { text('ignored') }`, map[string]Tool{"probe": func(context.Context, json.RawMessage) (any, error) {
		return nil, &UnconfirmedCleanupError{Cause: cause}
	}})
	if !errors.Is(err, cause) {
		t.Fatal(err)
	}
	err = (Runner{}).Run(context.Background(), `try { text('x') } catch(e) {}`, nil, func(Event) error { return cause })
	if !errors.Is(err, cause) {
		t.Fatal(err)
	}
	_, err = runOutput(context.Background(), Runner{}, `try { await tools.probe() } catch(e) {}`, map[string]Tool{"probe": func(context.Context, json.RawMessage) (any, error) { panic("child panic") }})
	var cleanup *UnconfirmedCleanupError
	if !errors.As(err, &cleanup) {
		t.Fatal(err)
	}
}

func TestRunnerSupportsConcurrentIndependentRuns(t *testing.T) {
	runner := Runner{}
	var group sync.WaitGroup
	for i := range 16 {
		group.Add(1)
		go func() {
			defer group.Done()
			output, err := runOutput(context.Background(), runner, fmt.Sprintf(`text(await tools.probe({value:%d}))`, i), map[string]Tool{"probe": func(_ context.Context, args json.RawMessage) (any, error) { return args, nil }})
			if err != nil || output != fmt.Sprintf("{\"value\":%d}\n", i) {
				t.Errorf("output=%q err=%v", output, err)
			}
		}()
	}
	group.Wait()
}
