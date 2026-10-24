package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jjyr/pot/pkg/agent"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/execution"
	m "github.com/jjyr/pot/pkg/modelprotocol"
	"github.com/jjyr/pot/pkg/protocol"
	"sync"
)

type Prepared struct {
	MessageCompleted func(message, text string)
	Agent            *agent.Agent
	Context          core.ExecutionContext
	Thread           core.Thread
	ResourceDigest   string
}

// Prepare pins local resources at original execution, then returns only
// run-bound model/approval/tool services owned by this node.
type Prepare func(context.Context, Run) (Prepared, error)
type Executor struct {
	Journal    *Journal
	Executions *execution.Manager
	Prepare    Prepare
	Context    context.Context
	mu         sync.Mutex
	active     map[string]context.CancelFunc
	stopped    bool
	wg         sync.WaitGroup
}

func key(control, task, run string) string {
	hash, _ := core.HashJSON([]string{control, task, run})
	return hash
}
func (e *Executor) Launch(ctx context.Context, launch Launch) (Run, error) {
	e.mu.Lock()
	stopped := e.stopped
	e.mu.Unlock()
	if stopped {
		return Run{}, fmt.Errorf("local task executor stopped")
	}
	if e.Journal == nil || e.Executions == nil || e.Prepare == nil || e.Context == nil {
		return Run{}, fmt.Errorf("task executor requires local execution services")
	}
	if launch.ControlID != e.Journal.NodeID {
		return Run{}, protocol.APIError{Code: "remote_execution_removed"}
	}
	record, err := e.Journal.Inspect(ctx, launch.ControlID, launch.TaskID, launch.RunID)
	if err != nil {
		return Run{}, err
	}
	if record == nil {
		return Run{}, fmt.Errorf("task run was not admitted")
	}
	hash, err := launch.Digest()
	if err != nil || hash != record.LaunchDigest {
		return Run{}, fmt.Errorf("task run differs from committed admission")
	}
	run := *record
	if run.Phase == Accepted {
		go func() { _, _ = e.Execute(e.Context, launch.ControlID, launch.TaskID, launch.RunID) }()
	}
	return run, nil
}
func (e *Executor) Inspect(ctx context.Context, control, task, run string) (*Run, error) {
	return e.Journal.Inspect(ctx, control, task, run)
}
func (e *Executor) Resume(ctx context.Context, control, task, run string) error {
	if e.Journal == nil || control != e.Journal.NodeID {
		return protocol.APIError{Code: "original_execution_unavailable"}
	}
	if e.Context == nil {
		return fmt.Errorf("local task lifecycle context unavailable")
	}
	if err := e.Journal.Resume(ctx, control, task, run); err != nil {
		return err
	}
	go func() { _, _ = e.Execute(e.Context, control, task, run) }()
	return nil
}
func (e *Executor) Stop(ctx context.Context, control, task, run, mode string) error {
	if e.Journal == nil || control != e.Journal.NodeID {
		return protocol.APIError{Code: "original_execution_unavailable"}
	}
	if mode != "interrupt" && mode != "cancel" {
		return fmt.Errorf("invalid stop mode")
	}
	value, err := e.Journal.Inspect(ctx, control, task, run)
	if err != nil {
		return err
	}
	if value == nil {
		return fmt.Errorf("task run not accepted")
	}
	if value.Phase.Ended() {
		return nil
	}
	if err := e.Journal.change(ctx, control, task, run, func(value *Run) error {
		if !value.Phase.Ended() {
			value.StopRequested = mode
		}
		return nil
	}); err != nil {
		return err
	}
	e.mu.Lock()
	cancel := e.active[key(control, task, run)]
	if cancel != nil {
		cancel()
	}
	e.mu.Unlock()
	if cancel == nil {
		if value.Phase == Running || value.Phase == RecoveryRequired {
			return core.ErrCleanupUnconfirmed
		}
		return e.Journal.change(ctx, control, task, run, func(value *Run) error {
			if value.Phase == Running {
				return core.ErrCleanupUnconfirmed
			}
			value.Phase = Cancelled
			value.Result = &Terminal{Outcome: core.Cancelled, Text: "Task stopped before execution."}
			value.CleanupConfirmed = true
			return nil
		})
	}
	return nil
}
func (e *Executor) Execute(ctx context.Context, control, task, runID string) (terminal *Terminal, result error) {
	if e.Journal == nil || control != e.Journal.NodeID {
		return nil, protocol.APIError{Code: "original_execution_unavailable"}
	}
	e.mu.Lock()
	if e.stopped {
		e.mu.Unlock()
		return nil, fmt.Errorf("local task executor stopped")
	}
	e.wg.Add(1)
	e.mu.Unlock()
	defer e.wg.Done()
	record, err := e.Journal.Inspect(ctx, control, task, runID)
	if err != nil {
		return nil, err
	}
	if record == nil {
		return nil, fmt.Errorf("task launch not accepted")
	}
	if err := record.Launch.Validate(e.Journal.NodeID); err != nil {
		return nil, err
	}
	claimed, err := e.Journal.Claim(ctx, control, task, runID)
	if err != nil {
		return nil, err
	}
	if !claimed {
		latest, err := e.Journal.Inspect(ctx, control, task, runID)
		if err != nil {
			return nil, err
		}
		return latest.Result, nil
	}
	ctx, cancel := context.WithCancel(ctx)
	runKey := key(control, task, runID)
	e.mu.Lock()
	if e.active == nil {
		e.active = map[string]context.CancelFunc{}
	}
	e.active[runKey] = cancel
	e.mu.Unlock()
	defer func() { cancel(); e.mu.Lock(); delete(e.active, runKey); e.mu.Unlock() }()
	var final Terminal
	var finalOutcome core.Outcome
	_, executionResult := e.Executions.Execute(ctx, execution.Identity{Owner: task, RequestID: runID, Generation: record.Launch.Generation, Role: "task"}, func(ctx context.Context) (outcome execution.Result) {
		defer func() {
			if outcome.Phase != execution.Interrupted {
				cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*1000*1000*1000)
				defer cancel()
				if err := e.Journal.Settle(cleanupCtx, control, task, runID); err != nil {
					outcome = execution.Result{Phase: execution.Interrupted, Err: errors.Join(core.ErrCleanupUnconfirmed, err)}
				}
			}
		}()
		current, err := e.Journal.Inspect(ctx, control, task, runID)
		if err != nil {
			return execution.Result{Phase: execution.Failed, Err: err}
		}
		if current.StopRequested != "" {
			final.Outcome = core.Cancelled
			return execution.Result{Phase: execution.Cancelled}
		}
		prepared, err := e.Prepare(ctx, *record)
		if err != nil {
			if core.IsCleanupUnconfirmed(err) {
				return execution.Result{Phase: execution.Interrupted, Err: err}
			}
			return execution.Result{Phase: execution.Failed, Err: err}
		}
		rejectPrepared := func(cause error) execution.Result {
			if prepared.Agent == nil || prepared.Agent.Cleanup == nil || prepared.Context.Owner != task {
				return execution.Result{Phase: execution.Interrupted, Err: errors.Join(core.ErrCleanupUnconfirmed, cause)}
			}
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*1000*1000*1000)
			defer cancel()
			if err := prepared.Agent.Cleanup(cleanupCtx, prepared.Context, true); err != nil {
				return execution.Result{Phase: execution.Interrupted, Err: errors.Join(core.ErrCleanupUnconfirmed, cause, err)}
			}
			return execution.Result{Phase: execution.Failed, Err: cause}
		}
		if prepared.Agent == nil || prepared.Context.Owner != task || prepared.Context.Role != core.Worker || prepared.Thread.Owner != task || prepared.Thread.Generation != record.Launch.Generation || prepared.Context.Workspace == "" || !digest(prepared.ResourceDigest) {
			return rejectPrepared(fmt.Errorf("invalid fixed task execution context"))
		}
		if err := prepared.Thread.Config.Validate(); err != nil {
			return rejectPrepared(err)
		}
		check := prepared.Thread.Config
		check.Params.Agent.Plugins = map[string]core.PluginConfig{}
		if err := check.Rehash(); err != nil || check.SpecHash != record.Launch.Config.SpecHash {
			return rejectPrepared(fmt.Errorf("prepared task configuration changed immutable launch"))
		}
		rawResources, pinned, err := e.Journal.PreparedResources(ctx, control, task, runID)
		if err != nil || pinned != prepared.ResourceDigest {
			return rejectPrepared(fmt.Errorf("prepared resource digest differs from original run"))
		}
		var pinnedConfig struct {
			Config core.ResolvedSpec `json:"config"`
		}
		if err := json.Unmarshal(rawResources, &pinnedConfig); err != nil {
			return rejectPrepared(err)
		}
		expected, err := core.HashJSON(pinnedConfig.Config)
		actual, actualErr := core.HashJSON(prepared.Thread.Config)
		if err != nil || actualErr != nil || expected != actual {
			return rejectPrepared(fmt.Errorf("prepared configuration differs from original resource evidence"))
		}
		for _, name := range record.Launch.Plugins {
			plugin, exists := prepared.Thread.Config.Params.Agent.Plugins[name]
			if !exists || !plugin.Enabled || plugin.Bundle == nil || plugin.Bundle.Manifest.ID != name {
				return rejectPrepared(fmt.Errorf("prepared plugin differs from selected name"))
			}
		}
		if len(prepared.Thread.Config.Params.Agent.Plugins) != len(record.Launch.Plugins) {
			return rejectPrepared(fmt.Errorf("prepared plugin set differs from launch"))
		}
		originalCleanup := prepared.Agent.Cleanup
		wrapped := *prepared.Agent
		wrapped.Cleanup = func(ctx context.Context, executionContext core.ExecutionContext, abort bool) error {
			if originalCleanup == nil {
				return core.ErrCleanupUnconfirmed
			}
			if err := originalCleanup(ctx, executionContext, abort); err != nil {
				return err
			}
			return e.Journal.RecordCleanup(ctx, control, task, runID)
		}
		engine := wrapped
		dispatch := engine.Dispatch
		engine.Dispatch = func(ctx context.Context, execution core.ExecutionContext, call m.ToolCall, emit func(core.Update) error) (json.RawMessage, error) {
			if err := e.checkCurrent(ctx, control, task, runID); err != nil {
				return nil, err
			}
			if isExecutionTool(call.Name, prepared.Thread.Config) {
				args, err := BindTarget(call.Arguments, record.Launch.ExecutionTarget)
				if err != nil {
					return nil, err
				}
				call.Arguments = args
			}
			if dispatch == nil {
				return nil, fmt.Errorf("authorized task dispatcher unavailable")
			}
			return dispatch(ctx, execution, call, emit)
		}
		resuming := false
		if len(record.Context) > 0 {
			var checkpoint agent.Checkpoint
			if json.Unmarshal(record.Context, &checkpoint) == nil {
				resuming = checkpoint.LastRequest == runID
			}
		}
		request := core.TurnRequest{RequestID: runID, Text: record.Launch.Text}
		if record.Launch.InputID != "" {
			request.Inputs = []core.ConversationInput{{ID: record.Launch.InputID, Text: record.Launch.Text}}
		}
		activeMessage := ""
		err = engine.Execute(ctx, prepared.Context, prepared.Thread, request, resuming, func(event core.AgentEvent) error {
			if err := e.Journal.AppendEvent(context.WithoutCancel(ctx), control, task, runID, event); err != nil {
				return err
			}

			if event.Update.Type == "message_started" {
				activeMessage = "message-" + event.Update.ID
			}
			if event.Update.Type == "message_finished" {
				// Earlier replies stay in the transcript; only the last reply is
				// the terminal summary, including an intentionally empty reply.
				final.Text = event.Update.Text
				if prepared.MessageCompleted != nil {
					prepared.MessageCompleted(activeMessage, event.Update.Text)
				}
			}
			if event.Update.Type == "turn_finished" && event.Update.Outcome != nil {
				finalOutcome = *event.Update.Outcome
			}
			return nil
		})
		var allowance protocol.APIError
		var allowancePointer *protocol.APIError
		if errors.As(err, &allowance) && allowance.Code == "pot_token_limit" || errors.As(err, &allowancePointer) && allowancePointer.Code == "pot_token_limit" {
			return execution.Result{Phase: execution.Failed, Err: err}
		}
		if finalOutcome.Kind == "unknown" || core.IsCleanupUnconfirmed(err) || errors.Is(err, agent.ErrUncertainEffects) {
			return execution.Result{Phase: execution.Interrupted, Err: err}
		}
		if errors.Is(err, context.Canceled) || finalOutcome.Kind == "cancelled" {
			final.Outcome = core.Cancelled
			return execution.Result{Phase: execution.Cancelled, Err: err}
		}
		if err != nil {
			final.Outcome = core.Outcome{Kind: "failed", Error: err.Error()}
			return execution.Result{Phase: execution.Failed, Err: err}
		}
		if finalOutcome.Kind != "success" {
			return execution.Result{Phase: execution.Interrupted, Err: fmt.Errorf("task completed without authoritative terminal outcome")}
		}
		final.Outcome = core.Success
		return execution.Result{Phase: execution.Succeeded}
	})
	cleanupCtx, cancelCleanup := context.WithTimeout(context.WithoutCancel(ctx), 5*1000*1000*1000)
	defer cancelCleanup()
	var allowance protocol.APIError
	var allowancePointer *protocol.APIError
	if errors.As(executionResult.Err, &allowance) && allowance.Code == "pot_token_limit" || errors.As(executionResult.Err, &allowancePointer) && allowancePointer.Code == "pot_token_limit" {
		if err := e.Journal.Wait(cleanupCtx, control, task, runID, executionResult.Err.Error()); err != nil {
			return nil, err
		}
		return nil, executionResult.Err
	}
	if executionResult.Phase == execution.Interrupted {
		_ = e.Journal.Fence(cleanupCtx, control, task, runID)
		return nil, errors.Join(core.ErrCleanupUnconfirmed, executionResult.Err)
	}
	if final.Outcome.Kind == "" {
		if executionResult.Phase == execution.Cancelled {
			final.Outcome = core.Cancelled
		} else {
			final.Outcome = core.Outcome{Kind: "failed", Error: fmt.Sprint(executionResult.Err)}
		}
	}
	if err := e.Journal.Finish(cleanupCtx, control, task, runID, final); err != nil {
		if core.IsCleanupUnconfirmed(err) {
			_ = e.Journal.Fence(cleanupCtx, control, task, runID)
		}
		return nil, err
	}
	return &final, executionResult.Err
}

// Close fences new invocations and waits for all admitted Agent cleanup before
// callers close the journal and injected model/approval services.
func (e *Executor) Close(ctx context.Context) error {
	e.mu.Lock()
	e.stopped = true
	for _, cancel := range e.active {
		cancel()
	}
	e.mu.Unlock()
	done := make(chan struct{})
	go func() { e.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return core.ErrCleanupUnconfirmed
	}
}
func (e *Executor) checkCurrent(ctx context.Context, control, task, run string) error {
	record, err := e.Journal.Inspect(ctx, control, task, run)
	if err != nil {
		return err
	}
	if record == nil || record.Phase != Running || record.BootID != e.Journal.BootID || record.StopRequested != "" || record.Finalizing {
		return fmt.Errorf("Task Agent run is fenced")
	}
	return nil
}
func isExecutionTool(name string, config core.ResolvedSpec) bool {
	if name == "exec_command" || name == "write_stdin" || name == "apply_patch" {
		return true
	}
	for _, plugin := range config.Params.Agent.Plugins {
		if !plugin.Enabled || plugin.Bundle == nil {
			continue
		}
		for _, raw := range plugin.Bundle.Manifest.Contributes.Tools.Tools {
			var definition m.ToolDefinition
			if json.Unmarshal(raw, &definition) == nil && definition.Name == name {
				return true
			}
		}
	}
	return false
}

// BindTarget independently prevents a worker from changing destination, even if
// the owner approval service would otherwise grant the requested operation.
func BindTarget(raw json.RawMessage, fixed core.ExecutionTarget) (json.RawMessage, error) {
	if err := fixed.Validate(); err != nil {
		return nil, err
	}
	var args map[string]json.RawMessage
	if err := core.DecodeJSON(raw, &args); err != nil {
		return nil, err
	}
	if _, exists := args["execution_target"]; exists {
		return nil, fmt.Errorf("worker cannot select an execution target")
	}
	return raw, nil
}
