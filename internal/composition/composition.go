// Package composition runs capability-only JavaScript tool compositions.
//
// Each Run owns a fresh goja runtime on its calling goroutine. Tool calls execute
// concurrently, but JSON conversion and Promise resolution happen on that owner.
// No filesystem, network, process, module, timer, or Node bindings are installed.
//
// goja does not have a hard per-runtime heap limit. These limits constrain inputs,
// outputs, work and time, but do not make untrusted JavaScript memory-safe for its
// hosting process. Production adapters use ProcessRunner to isolate the VM from
// the parent heap. Its sampled memory cancellation is not a hard heap/RSS cap.
package composition

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/dop251/goja"
	"github.com/jjyr/pot/internal/core"
)

var (
	ErrCodeLimit        = errors.New("composition code exceeds limit")
	ErrOutputLimit      = errors.New("composition output exceeds limit")
	ErrCallLimit        = errors.New("composition tool call limit reached")
	ErrConcurrencyLimit = errors.New("composition concurrent tool limit reached")
	ErrJSONLimit        = errors.New("composition tool JSON exceeds limit")
	ErrScriptTimeLimit  = errors.New("composition uninterrupted JavaScript time limit reached")
	ErrWallTimeLimit    = errors.New("composition wall time limit reached")
)

// Tool is an already authorized dispatch path, not an authorization mechanism.
// Callers must supply only capabilities available to the current role and apply
// normal approval, destination, receipt and policy checks within each Tool.
// Implementations must honor ctx and confirm their own resource cleanup. Context
// cancellation does not roll back side effects or prove that cleanup succeeded.
// Arguments and results use JSON, never live VM values.
type Tool func(context.Context, json.RawMessage) (any, error)

// UnconfirmedCleanupError prevents JavaScript from hiding uncertain cleanup by
// catching a tool rejection. The ordinary execution layer owns the recovery fence.
type UnconfirmedCleanupError struct{ Cause error }

func (e *UnconfirmedCleanupError) Error() string {
	return fmt.Sprintf("composition tool cleanup unconfirmed: %v", e.Cause)
}

func (e *UnconfirmedCleanupError) Unwrap() error            { return e.Cause }
func (e *UnconfirmedCleanupError) CleanupUnconfirmed() bool { return true }

// Limits uses the former Rust composition limits where equivalent controls exist.
// StackDepth is a goja call-depth bound, not the former QuickJS byte stack size.
// JSONBytes bounds both each tool argument and each result after serialization.
// Zero fields select defaults; negative fields are invalid.
type Limits struct {
	CodeBytes       int
	OutputBytes     int
	JSONBytes       int
	Calls           int
	ConcurrentCalls int
	StackDepth      int
	JavaScriptTime  time.Duration
	WallTime        time.Duration
}

// Runner is reusable for independent runs. Configure it before concurrent use.
// The zero value uses 64 KiB code/output, 4 MiB JSON values, 128 calls, 8 concurrent
// calls, 512 stack frames, 2 seconds uninterrupted JS, and 10 minutes wall time.
type Runner struct{ Limits Limits }

type completion struct {
	id    int
	value []byte
	err   error
}

type pendingCall struct {
	resolve func(any) error
	reject  func(any) error
}

// Run executes code with top-level await, tools.NAME(args) and text(value).
// Only text output reaches emit. Each emitted string includes a trailing newline;
// chunks are ordered, incremental and never replayed. emit runs on the VM owner,
// must return promptly, and may be nil to discard explicit output.
//
// Run returns once the script settles or fails, cancelling all unfinished tools.
// It does not wait indefinitely for tools that ignore cancellation; the execution
// layer must track and fence uncertain operations. Unawaited calls can already
// have effects and are cancelled at script completion. Recursive exec/wait are
// rejected even if accidentally included in tools.
func (r Runner) Run(ctx context.Context, code string, tools map[string]Tool, emit func(string) error) error {
	limits, err := r.Limits.defaults()
	if err != nil {
		return err
	}
	if code == "" || len(code) > limits.CodeBytes {
		return ErrCodeLimit
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	for name, tool := range tools {
		if name == "exec" || name == "wait" || name == "" || tool == nil {
			return fmt.Errorf("invalid composition capability %q", name)
		}
	}
	ctx, cancel := context.WithTimeoutCause(ctx, limits.WallTime, ErrWallTimeLimit)
	defer cancel()
	vm := goja.New()
	vm.SetMaxCallStackSize(limits.StackDepth)
	toString, _ := goja.AssertFunction(vm.Get("String"))
	messageForError := func(err error) string {
		if exception, ok := err.(*goja.Exception); ok {
			value, conversionErr := toString(goja.Undefined(), exception.Value())
			if conversionErr == nil {
				return value.String()
			}
			return "JavaScript exception (message unavailable)"
		}
		return err.Error()
	}
	stopped := make(chan struct{})
	defer close(stopped)
	go func() {
		select {
		case <-ctx.Done():
			vm.Interrupt(context.Cause(ctx)) // goja's explicitly thread-safe operation.
		case <-stopped:
		}
	}()
	// A timer covers each entry into JS, including its microtask drain. Waiting for
	// external tools does not consume the uninterrupted JavaScript allowance.
	enter := func(fn func() error) error {
		var mu sync.Mutex
		active := true
		expired := false
		timer := time.AfterFunc(limits.JavaScriptTime, func() {
			mu.Lock()
			defer mu.Unlock()
			if active {
				expired = true
				vm.Interrupt(ErrScriptTimeLimit)
			}
		})
		err := fn()
		// Never return VM-backed exceptions: formatting an arbitrary thrown object
		// can itself execute JavaScript. Convert it under this same time budget.
		if _, ok := err.(*goja.Exception); ok {
			err = fmt.Errorf("composition JavaScript: %s", messageForError(err))
		}
		mu.Lock()
		active = false
		timer.Stop()
		if expired {
			err = ErrScriptTimeLimit
		}
		mu.Unlock()
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		return err
	}

	// Capture intrinsic JSON operations before user code can modify globals.
	stringify, _ := goja.AssertFunction(vm.Get("JSON").ToObject(vm).Get("stringify"))
	parse, _ := goja.AssertFunction(vm.Get("JSON").ToObject(vm).Get("parse"))
	completed := make(chan completion, limits.ConcurrentCalls)
	pending := make(map[int]pendingCall)
	calls, outputBytes := 0, 0
	var fatal error
	toolObject := vm.NewObject()
	if err := toolObject.SetPrototype(nil); err != nil {
		return err
	}
	for name, tool := range tools {
		if err := toolObject.Set(name, func(call goja.FunctionCall) goja.Value {
			promise, resolve, reject := vm.NewPromise()
			fail := func(err error) goja.Value {
				_ = reject(vm.NewTypeError("%s", messageForError(err)))
				return vm.ToValue(promise)
			}
			if fatal != nil {
				return fail(fatal)
			}
			if ctx.Err() != nil {
				return fail(context.Cause(ctx))
			}
			if calls >= limits.Calls {
				return fail(ErrCallLimit)
			}
			if len(pending) >= limits.ConcurrentCalls {
				return fail(ErrConcurrencyLimit)
			}
			args := call.Argument(0)
			if goja.IsUndefined(args) {
				args = vm.NewObject()
			}
			encoded, err := stringify(goja.Undefined(), args)
			if err != nil {
				return fail(err)
			}
			if goja.IsUndefined(encoded) {
				return fail(errors.New("tool arguments must serialize to JSON"))
			}
			data := []byte(encoded.String())
			if len(data) > limits.JSONBytes {
				return fail(ErrJSONLimit)
			}
			calls++
			id := calls
			pending[id] = pendingCall{resolve: resolve, reject: reject}
			go func() {
				result := invoke(ctx, tool, data, limits.JSONBytes)
				result.id = id
				select {
				case completed <- result:
				case <-ctx.Done():
				}
			}()
			return vm.ToValue(promise)
		}); err != nil {
			return err
		}
	}
	freeze, err := vm.RunString("Object.freeze")
	if err != nil {
		return err
	}
	freezeFn, _ := goja.AssertFunction(freeze)
	if _, err := freezeFn(goja.Undefined(), toolObject); err != nil {
		return err
	}
	text := vm.ToValue(func(call goja.FunctionCall) goja.Value {
		if fatal != nil {
			panic(vm.NewTypeError("%s", messageForError(fatal)))
		}
		value := call.Argument(0)
		var output string
		if _, ok := value.(goja.String); ok {
			output = value.String()
		} else {
			encoded, err := stringify(goja.Undefined(), value)
			if err != nil {
				panic(vm.NewTypeError("%s", messageForError(err)))
			}
			output = encoded.String()
		}
		outputBytes += len(output) + 1
		if outputBytes > limits.OutputBytes {
			fatal = ErrOutputLimit
			panic(vm.NewTypeError("%s", messageForError(fatal)))
		}
		if emit != nil {
			if err := emit(output + "\n"); err != nil {
				fatal = err
				panic(vm.NewTypeError("%s", messageForError(err)))
			}
		}
		return goja.Undefined()
	})
	var promise *goja.Promise
	if err := enter(func() error {
		value, err := vm.RunString("(async function(tools, text) {\n'use strict';\n" + code + "\n})")
		if err != nil {
			return err
		}
		fn, _ := goja.AssertFunction(value)
		value, err = fn(goja.Undefined(), toolObject, text)
		if err == nil {
			promise = value.Export().(*goja.Promise)
		}
		return err
	}); err != nil {
		return err
	}
	for {
		if fatal != nil {
			return fatal
		}
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		switch promise.State() {
		case goja.PromiseStateFulfilled:
			return nil
		case goja.PromiseStateRejected:
			var message string
			if err := enter(func() error {
				value, err := toString(goja.Undefined(), promise.Result())
				if err == nil {
					message = value.String()
				}
				return err
			}); err != nil {
				return err
			}
			return fmt.Errorf("composition JavaScript: %s", message)
		}
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case result := <-completed:
			entry := pending[result.id]
			delete(pending, result.id)
			var cleanup *UnconfirmedCleanupError
			if errors.As(result.err, &cleanup) {
				return result.err
			}
			if err := enter(func() error {
				if result.err != nil {
					return entry.reject(vm.NewTypeError("%s", messageForError(result.err)))
				}
				value, err := parse(goja.Undefined(), vm.ToValue(string(result.value)))
				if err != nil {
					return err
				}
				return entry.resolve(value)
			}); err != nil {
				return err
			}
		}
	}
}

func invoke(ctx context.Context, tool Tool, args json.RawMessage, jsonLimit int) (result completion) {
	defer func() {
		if value := recover(); value != nil {
			result.err = &UnconfirmedCleanupError{Cause: fmt.Errorf("tool panic: %v", value)}
		}
	}()
	value, err := tool(ctx, args)
	if err != nil {
		if core.IsCleanupUnconfirmed(err) {
			err = &UnconfirmedCleanupError{Cause: err}
		}
		result.err = err
		return result
	}
	result.value, result.err = json.Marshal(value)
	if len(result.value) > jsonLimit {
		result.value, result.err = nil, ErrJSONLimit
	}
	return result
}

func (l Limits) defaults() (Limits, error) {
	ints := []struct {
		value    *int
		fallback int
	}{
		{&l.CodeBytes, 65536}, {&l.OutputBytes, 65536}, {&l.JSONBytes, 4 * 1024 * 1024},
		{&l.Calls, 128}, {&l.ConcurrentCalls, 8}, {&l.StackDepth, 512},
	}
	for _, field := range ints {
		if *field.value < 0 {
			return l, errors.New("composition limits must be positive")
		}
		if *field.value == 0 {
			*field.value = field.fallback
		}
	}
	if l.JavaScriptTime < 0 || l.WallTime < 0 {
		return l, errors.New("composition time limits must be positive")
	}
	if l.JavaScriptTime == 0 {
		l.JavaScriptTime = 2 * time.Second
	}
	if l.WallTime == 0 {
		l.WallTime = 10 * time.Minute
	}
	return l, nil
}
