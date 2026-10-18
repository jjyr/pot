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
	yield  chan struct{}
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
	delay := uint64(30000)
	if name == "wait" {
		delay = 10000
	}
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
		active := 0
		for _, job := range manager.jobs {
			select {
			case <-job.done:
			default:
				active++
			}
		}
		if active >= 4 {
			return nil, fmt.Errorf("composition active run limit reached; wait for an existing exec_id")
		}
		if len(manager.jobs) >= 64 {
			return nil, fmt.Errorf("composition retained result limit reached; collect existing exec_id results")
		}
		id = newID()
		ctx, cancel := context.WithCancel(manager.ctx)
		job := &compositionJob{done: make(chan struct{}), yield: make(chan struct{}, 1), cancel: cancel}
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
			err := manager.composer.Run(ctx, args.Code, tools, func(event composition.Event) error {
				job.mu.Lock()
				defer job.mu.Unlock()
				switch event.Type {
				case "text":
					if job.output.Len()+len(event.Text) > 65536 {
						return composition.ErrOutputLimit
					}
					job.output.WriteString(event.Text)
				case "yield":
					select {
					case job.yield <- struct{}{}:
					default:
					}
				default:
					return fmt.Errorf("invalid composition event %q", event.Type)
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
	select {
	case <-manager.ctx.Done():
		return nil, manager.ctx.Err()
	case <-job.done:
	case <-job.yield:
	case <-timer.C:
	}
	job.mu.Lock()
	done := false
	select {
	case <-job.done:
		done = true
	default:
	}
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
	if done {
		job.cancel()
		delete(manager.jobs, id)
	}
	return json.Marshal(value)
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
	return []m.ToolDefinition{{Name: "exec", Description: "Compose authorized tools using JavaScript with top-level await, tools.NAME(args), and text(value). Await independent calls with Promise.all. text(value) appends output without yielding. yield_control() returns accumulated output immediately while the script keeps running. Otherwise return on completion or the yield timeout. No filesystem/network/process/module/timer/Node globals or recursive exec/wait. Up to 4 active runs at once (completed runs release capacity; no cumulative turn quota), up to 64 uncollected runs/results, 128 calls/run, 8 concurrent calls, 64 KiB code/output, 2s uninterrupted JS and 10min wall time. Disposable Go worker uses a 64 MiB soft memory target and sampled 96 MiB live-heap cancellation; neither is a hard heap/RSS cap. Unawaited effects may have occurred; runs cancel at turn end. exec waits up to 30000 ms by default; shorter scripts return as soon as they finish. Set yield_time_ms only to request a different wait. Returns exec_id only while running; collect it with wait before ending the turn.", InputSchema: json.RawMessage(`{"type":"object","properties":{"code":{"type":"string","maxLength":65536},"yield_time_ms":{"type":"integer","minimum":1,"maximum":60000,"description":"Maximum wait before yielding a still-running script; completed scripts return immediately. Defaults to 30000 ms."}},"required":["code"],"additionalProperties":false}`)}, {Name: "wait", Description: "Collect only new explicit output or completion from an exec_id in this turn. Unknown/expired IDs fail. Defaults to a 10000 ms wait; text output alone does not yield early. Use only after exec returns completed:false and an exec_id. This does not poll shell sessions or Tasks.", InputSchema: json.RawMessage(`{"type":"object","properties":{"exec_id":{"type":"string"},"yield_time_ms":{"type":"integer","minimum":1,"maximum":60000,"description":"Maximum wait for completion or an explicit yield_control(); defaults to 10000 ms."}},"required":["exec_id"],"additionalProperties":false}`)}}
}
