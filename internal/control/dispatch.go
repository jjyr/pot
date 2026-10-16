package control

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jjyr/pot/internal/agent"
	"github.com/jjyr/pot/internal/composition"
	"github.com/jjyr/pot/internal/core"
	"github.com/jjyr/pot/internal/executor"
	"github.com/jjyr/pot/internal/modelprotocol"
)

type ToolEngine interface {
	Execute(context.Context, executor.Request, func(executor.Event) error) (map[string]any, error)
}
type TargetDispatch func(context.Context, Operation, executor.Request, func(core.Update) error) (json.RawMessage, error)

// Dispatcher is fixed to one run. Every ordinary and composition child operation
// reaches this same target, approval and receipt boundary.
type Dispatcher struct {
	RuntimeID, Owner, Turn string
	Generation             uint64
	DefaultTarget          core.ExecutionTarget
	Spec                   core.ResolvedSpec
	Permissions            *PermissionStore
	Operations             *Operations
	Host                   ToolEngine
	Target                 TargetDispatch
	Product                map[string]agent.Dispatch
}

func (d *Dispatcher) Dispatch(ctx context.Context, execution core.ExecutionContext, call modelprotocol.ToolCall, emit func(core.Update) error) (json.RawMessage, error) {
	if execution.Owner != d.Owner || d.Owner == "" || d.Turn == "" || d.Generation == 0 || d.Permissions == nil || d.Operations == nil {
		return nil, errors.New("execution dispatch owner is unavailable")
	}
	if handler := d.Product[call.Name]; handler != nil {
		return handler(ctx, execution, call, emit)
	}
	args := map[string]any{}
	if err := core.DecodeJSON(call.Arguments, &args); err != nil {
		return nil, err
	}
	target, err := d.target(args["execution_target"])
	if err != nil {
		return nil, err
	}
	delete(args, "execution_target")
	if _, _, err = d.Permissions.Policy(target.RuntimeID); err != nil {
		return nil, err
	}
	prepared, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	operation := Operation{Owner: d.Owner, Turn: d.Turn, Generation: d.Generation, CallID: call.ID, Target: target, Tool: call.Name, Args: prepared}
	key, err := operation.Key()
	if err != nil {
		return nil, err
	}
	hash, err := core.HashJSON(operation)
	if err != nil {
		return nil, err
	}
	existing, err := d.Operations.Get(ctx, key)
	if err == nil {
		if existing.InputHash != hash {
			return nil, errors.New("operation identity rebound")
		}
		if existing.State != "complete" {
			return nil, &composition.UnconfirmedCleanupError{Cause: ErrOperationUnknown}
		}
		if existing.Error != "" {
			return existing.Result, errors.New(existing.Error)
		}
		return existing.Result, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if target.Target.Kind == "host" {
		_, epoch, err := d.Permissions.Policy(target.RuntimeID)
		if err != nil {
			return nil, err
		}
		if _, err = d.Permissions.Authorize(ctx, HostRequest{Owner: d.Owner, Turn: d.Turn, Generation: d.Generation, CallID: call.ID, MachineID: target.RuntimeID, EnrollmentEpoch: epoch, Tool: call.Name, Args: prepared}); err != nil {
			return nil, err
		}
	}
	name, engineArgs, err := d.prepare(call.Name, args, target)
	if err != nil {
		return nil, err
	}
	receipt, claimed, err := d.Operations.Begin(ctx, operation)
	if err != nil {
		if errors.Is(err, ErrOperationUnknown) {
			return nil, &composition.UnconfirmedCleanupError{Cause: err}
		}
		return nil, err
	}
	if !claimed {
		if receipt.Error != "" {
			return receipt.Result, errors.New(receipt.Error)
		}
		return receipt.Result, nil
	}
	request := executor.Request{ID: call.ID, Operation: name, CWD: execution.Workspace, Policy: d.Spec.Params.Agent.Shell, Args: engineArgs}
	var value json.RawMessage
	if target.RuntimeID == d.RuntimeID && target.Target.Kind == "host" {
		if d.Host == nil {
			err = errors.New("Host executor is unavailable")
		} else {
			var result map[string]any
			result, err = d.Host.Execute(ctx, request, func(event executor.Event) error {
				if emit == nil {
					return nil
				}
				return emit(core.Update{Type: "tool_output", ID: event.ToolOutput.ID, Stream: event.ToolOutput.Stream, Text: event.ToolOutput.Text})
			})
			if result != nil {
				value, _ = json.Marshal(result)
			}
		}
	} else if d.Target != nil {
		value, err = d.Target(ctx, operation, request, emit)
	} else {
		err = errors.New("execution destination is unavailable; no fallback")
	}
	settle, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if core.IsCleanupUnconfirmed(err) {
		d.Operations.Unknown(settle, key)
		return nil, &composition.UnconfirmedCleanupError{Cause: err}
	}
	if persistErr := d.Operations.Complete(settle, key, value, err); persistErr != nil {
		d.Operations.Unknown(settle, key)
		return nil, &composition.UnconfirmedCleanupError{Cause: persistErr}
	}
	return value, err
}
func (d *Dispatcher) target(selector any) (core.ExecutionTarget, error) {
	target := d.DefaultTarget
	switch v := selector.(type) {
	case nil:
	case string:
		switch {
		case v == "host":
			target = core.HostTarget(d.DefaultTarget.RuntimeID)
		case strings.HasPrefix(v, "sandbox:"):
			target = core.SandboxTarget(d.DefaultTarget.RuntimeID, strings.TrimPrefix(v, "sandbox:"))
		default:
			return target, errors.New("invalid execution target selector")
		}
	default:
		data, err := json.Marshal(selector)
		if err != nil {
			return target, err
		}
		if err = core.DecodeJSON(data, &target); err != nil {
			return target, err
		}
	}
	return target, target.Validate()
}
func (d *Dispatcher) prepare(name string, args map[string]any, target core.ExecutionTarget) (string, map[string]any, error) {
	switch name {
	case "exec_command", "write_stdin", "apply_patch":
		return name, args, nil
	}
	for _, config := range d.Spec.Params.Agent.Plugins {
		if !config.Enabled || config.Bundle == nil {
			continue
		}
		bundle := config.Bundle
		for _, definition := range bundle.Manifest.Tools {
			var tool struct {
				Name string `json:"name"`
			}
			if json.Unmarshal(definition, &tool) != nil || tool.Name != name {
				continue
			}
			if err := bundle.ValidateInput(name, args); err != nil {
				return "", nil, err
			}
			if target.Target.Kind == "host" && bundle.Manifest.Execution != "host_or_sandbox" {
				return "", nil, errors.New("plugin supports Sandbox execution only")
			}
			mode := "text"
			if d.Spec.Params.Agent.ModelCatalog != nil {
				descriptor, err := d.Spec.Params.Agent.ModelCatalog.Resolve(d.Spec.Provider, d.Spec.Model)
				if err != nil {
					return "", nil, err
				}
				if descriptor.Vision() {
					mode = "mixed"
				}
			}
			envelope := map[string]any{"plugin": bundle, "tool": name, "input": args, "mode": mode}
			if session, ok := args["session_id"]; ok {
				envelope["session_id"] = session
			}
			return "plugin_call", envelope, nil
		}
	}
	return "", nil, fmt.Errorf("tool %s is not registered for this execution", name)
}
