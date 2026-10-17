package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jjyr/pot/pkg/composition"
	"github.com/jjyr/pot/pkg/core"
	m "github.com/jjyr/pot/pkg/modelprotocol"
	"strings"
	"sync"
	"time"
)

type compositionJob struct {
	mu     sync.Mutex
	output strings.Builder
	err    error
	done   chan struct{}
	notify chan struct{}
	cancel context.CancelFunc
}
type compositionManager struct {
	ctx       context.Context
	composer  Composer
	dispatch  Dispatch
	execution core.ExecutionContext
	emit      func(core.Update) error
	tools     []m.ToolDefinition
	jobs      map[string]*compositionJob
}

func (manager *compositionManager) call(name string, raw json.RawMessage, parent string) (json.RawMessage, error) {
	var args struct {
		Code        string  `json:"code"`
		ExecID      string  `json:"exec_id"`
		YieldTimeMS *uint64 `json:"yield_time_ms"`
	}
	if err := core.DecodeJSON(raw, &args); err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	for key := range fields {
		if key != "yield_time_ms" && !(name == "exec" && key == "code") && !(name == "wait" && key == "exec_id") {
			return nil, fmt.Errorf("invalid composition argument %s", key)
		}
	}
	delay := uint64(1000)
	if args.YieldTimeMS != nil {
		delay = *args.YieldTimeMS
	}
	if delay < 1 || delay > 60000 {
		return nil, fmt.Errorf("invalid yield_time_ms")
	}
	id := args.ExecID
	if name == "exec" {
		if args.Code == "" || len(args.Code) > 65536 {
			return nil, composition.ErrCodeLimit
		}
		if len(manager.jobs) >= 4 {
			return nil, fmt.Errorf("composition turn run limit reached")
		}
		id = newID()
		ctx, cancel := context.WithCancel(manager.ctx)
		job := &compositionJob{done: make(chan struct{}), notify: make(chan struct{}, 1), cancel: cancel}
		manager.jobs[id] = job
		tools := map[string]composition.Tool{}
		var ids sync.Mutex
		next := 0
		for _, definition := range manager.tools {
			if definition.Name == "exec" || definition.Name == "wait" {
				continue
			}
			toolName := definition.Name
			tools[toolName] = func(ctx context.Context, args json.RawMessage) (any, error) {
				ids.Lock()
				next++
				callID := fmt.Sprintf("%s:js:%d", parent, next)
				ids.Unlock()
				call := m.ToolCall{ID: callID, Name: toolName, Arguments: args}
				if err := manager.emit(core.Update{Type: "tool_started", ParentID: parent, ID: callID, Name: toolName, Args: args}); err != nil {
					return nil, err
				}
				value, err := manager.dispatch(ctx, manager.execution, call, manager.emit)
				if err != nil {
					if core.IsCleanupUnconfirmed(err) {
						return nil, &composition.UnconfirmedCleanupError{Cause: err}
					}
					failure := toolFailure(value, err)
					if emitErr := manager.emit(core.Update{Type: "tool_finished", ID: callID, Name: toolName, Result: failure, Failed: true}); emitErr != nil {
						return nil, emitErr
					}
					return nil, err
				}
				if !json.Valid(value) {
					return nil, fmt.Errorf("invalid composition child JSON result")
				}
				if err := manager.emit(core.Update{Type: "tool_finished", ID: callID, Name: toolName, Result: value, Failed: failedResult(value)}); err != nil {
					return nil, err
				}
				return json.RawMessage(value), nil
			}
		}
		go func() {
			defer close(job.done)
			err := manager.composer.Run(ctx, args.Code, tools, func(chunk string) error {
				job.mu.Lock()
				defer job.mu.Unlock()
				if job.output.Len()+len(chunk) > 65536 {
					return composition.ErrOutputLimit
				}
				job.output.WriteString(chunk)
				select {
				case job.notify <- struct{}{}:
				default:
				}
				return nil
			})
			job.mu.Lock()
			job.err = err
			job.mu.Unlock()
		}()
	}
	job := manager.jobs[id]
	if job == nil {
		return nil, fmt.Errorf("unknown or expired exec_id")
	}
	timer := time.NewTimer(time.Duration(delay) * time.Millisecond)
	defer timer.Stop()
	for {
		job.mu.Lock()
		done := false
		select {
		case <-job.done:
			done = true
		default:
		}
		if done || job.output.Len() > 0 {
			value := map[string]any{"output": job.output.String(), "completed": done}
			job.output.Reset()
			if !done {
				value["exec_id"] = id
			}
			if job.err != nil {
				var uncertain *composition.UnconfirmedCleanupError
				if errors.As(job.err, &uncertain) {
					job.mu.Unlock()
					return nil, job.err
				}
				value["isError"] = true
				value["error"] = job.err.Error()
			}
			job.mu.Unlock()
			return json.Marshal(value)
		}
		job.mu.Unlock()
		select {
		case <-manager.ctx.Done():
			return nil, manager.ctx.Err()
		case <-job.notify:
		case <-job.done:
		case <-timer.C:
			job.mu.Lock()
			value := map[string]any{"output": job.output.String(), "completed": false, "exec_id": id}
			job.output.Reset()
			job.mu.Unlock()
			return json.Marshal(value)
		}
	}
}
func (manager *compositionManager) close() error {
	for _, job := range manager.jobs {
		job.cancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, job := range manager.jobs {
		select {
		case <-job.done:
			job.mu.Lock()
			err := job.err
			job.mu.Unlock()
			var uncertain *composition.UnconfirmedCleanupError
			if errors.As(err, &uncertain) {
				return err
			}
		case <-ctx.Done():
			return &composition.UnconfirmedCleanupError{Cause: fmt.Errorf("composition child cleanup did not settle")}
		}
	}
	return nil
}
func compositionSchemas() []m.ToolDefinition {
	return []m.ToolDefinition{{Name: "exec", Description: "Compose authorized tools using JavaScript with top-level await, tools.NAME(args), and text(value). Await independent calls with Promise.all. Only text output is published. No filesystem/network/process/module/timer/Node globals or recursive exec/wait. Up to 4 runs/turn, 128 calls/run, 8 concurrent calls, 64 KiB code/output, 2s uninterrupted JS and 10min wall time. Disposable Go worker uses a 64 MiB soft memory target and sampled 96 MiB live-heap cancellation; neither is a hard heap/RSS cap. Unawaited effects may have occurred; runs cancel at turn end. Returns exec_id for wait while running.", InputSchema: json.RawMessage(`{"type":"object","properties":{"code":{"type":"string","maxLength":65536},"yield_time_ms":{"type":"integer","minimum":1,"maximum":60000}},"required":["code"],"additionalProperties":false}`)}, {Name: "wait", Description: "Collect only new explicit output or completion from an exec_id in this turn. Unknown/expired IDs fail. This does not poll shell sessions or Tasks.", InputSchema: json.RawMessage(`{"type":"object","properties":{"exec_id":{"type":"string"},"yield_time_ms":{"type":"integer","minimum":1,"maximum":60000}},"required":["exec_id"],"additionalProperties":false}`)}}
}
