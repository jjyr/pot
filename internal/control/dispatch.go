package control

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/jjyr/pot/internal/usage"
	"github.com/jjyr/pot/pkg/agent"
	"github.com/jjyr/pot/pkg/composition"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/executor"
	"github.com/jjyr/pot/pkg/integrations"
	"github.com/jjyr/pot/pkg/modelprotocol"
	"github.com/jjyr/pot/pkg/storage"
)

type ToolEngine interface {
	Execute(context.Context, executor.Request, func(executor.Event) error) (map[string]any, error)
}

// Dispatcher is fixed to one run. Every ordinary and composition child operation
// reaches this same target, approval and receipt boundary.
type Dispatcher struct {
	RuntimeID, Owner, Turn string
	ApprovalOwner, Task    string
	Generation             uint64
	DefaultTarget          core.ExecutionTarget
	HostWorkspace          string
	Usage                  *usage.Ledger
	UsageScope             string
	Spec                   core.ResolvedSpec
	Permissions            *PermissionStore
	Operations             *Operations
	Host                   ToolEngine
	ResolveCommand         func(context.Context, map[string]any) (*CommandContext, error)

	Credentials integrations.CredentialResolver
	Product     map[string]agent.Dispatch
}

func (d *Dispatcher) Dispatch(ctx context.Context, execution core.ExecutionContext, call modelprotocol.ToolCall, emit func(core.Update) error) (output json.RawMessage, resultErr error) {
	if execution.Owner != d.Owner || d.Owner == "" || d.Turn == "" || d.Generation == 0 || d.Permissions == nil || d.Operations == nil {
		return nil, errors.New("execution dispatch owner is unavailable")
	}
	if d.Usage != nil {
		scope := d.UsageScope
		if scope == "" {
			scope = d.Owner
		}
		role := "main"
		if execution.Role == core.Worker {
			role = "task"
		}
		callTokens := int64(agent.EstimateToolCallTokens(call))
		if err := d.Usage.Tool(ctx, scope, d.Turn, usage.ToolEvent{CallID: call.ID, Name: call.Name, Action: "start", Arguments: call.Arguments, Agent: role, EstimatedTokens: &callTokens, EstimateMethod: agent.ContentEstimateMethod}, storage.Now()); err != nil {
			return nil, err
		}
		defer func() {
			data := output
			if len(data) == 0 {
				data = json.RawMessage("null")
			}
			var resultTokens *int64
			if n, err := agent.EstimateToolResultTokens(call.ID, data); err == nil {
				value := int64(n)
				resultTokens = &value
			}
			err := d.Usage.Tool(context.WithoutCancel(ctx), scope, d.Turn, usage.ToolEvent{CallID: call.ID, Name: call.Name, Action: "end", Content: data, EstimatedTokens: resultTokens, EstimateMethod: agent.ContentEstimateMethod, IsError: resultErr != nil || agent.ToolResultFailed(data)}, storage.Now())
			resultErr = errors.Join(resultErr, err)
		}()
	}
	executionMode := ""
	executionAttempted := false
	defer func() {
		if resultErr == nil {
			return
		}
		value := map[string]any{}
		_ = core.DecodeJSON(output, &value)
		if value == nil {
			value = map[string]any{}
		}
		value["isError"] = true
		value["error"] = resultErr.Error()
		if executionMode != "" && executionMode != "session" && value["execution_mode"] == nil {
			value["execution_mode"] = executionMode
		}
		var uncertain *composition.UnconfirmedCleanupError
		if core.IsCleanupUnconfirmed(resultErr) || errors.As(resultErr, &uncertain) {
			value["execution_status"] = "cleanup_unconfirmed"
		} else if value["execution_status"] == nil {
			value["execution_status"] = "not_started"
			if executionAttempted {
				value["execution_status"] = "failed"
			}
		}
		output, _ = json.Marshal(value)
	}()
	state := func(phase string, target *core.ExecutionTarget, workdir string) error {
		if emit == nil {
			return nil
		}
		return emit(core.Update{Type: "tool_state", ID: call.ID, Phase: phase, ExecutionMode: executionMode, Target: target, Workdir: workdir})
	}
	if handler := d.Product[call.Name]; handler != nil {
		if err := state("running", nil, ""); err != nil {
			return nil, err
		}
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
	if target.RuntimeID != d.RuntimeID {
		return nil, errors.New("execution target must be the local node")
	}
	delete(args, "execution_target")
	name, engineArgs, err := d.prepare(call.Name, args, target)
	if err != nil {
		return nil, err
	}
	cwd := execution.Workspace
	if target.IsLocalTo(d.RuntimeID) && d.HostWorkspace != "" {
		cwd = d.HostWorkspace
	}
	displayCWD := cwd
	if dir, ok := args["workdir"].(string); ok && dir != "" {
		if filepath.IsAbs(dir) {
			displayCWD = dir
		} else {
			displayCWD = filepath.Join(cwd, dir)
		}
	}
	if err := state("preparing", &target, displayCWD); err != nil {
		return nil, err
	}

	if _, _, err = d.Permissions.Policy(target.RuntimeID); err != nil {
		return nil, err
	}
	prepared, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	operation := Operation{Owner: d.Owner, Turn: d.Turn, Generation: d.Generation, CallID: call.ID, Target: target, Tool: call.Name, Args: prepared, Worker: execution.Role == core.Worker}
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

	permissionPolicy, epoch, err := d.Permissions.Policy(target.RuntimeID)
	if err != nil {
		return nil, err
	}
	approvalOwner := d.ApprovalOwner
	if approvalOwner == "" {
		approvalOwner = d.Owner
	}
	var commandContext *CommandContext
	if call.Name == "exec_command" {
		if d.ResolveCommand != nil {
			commandContext, err = d.ResolveCommand(ctx, args)
		} else if target.IsLocalTo(d.RuntimeID) {
			commandContext, err = ResolveCommandContext(cwd, args, d.Spec.Params.Agent.Shell)
		} else {
			err = errors.New("destination command resolver is unavailable")
		}
		if err != nil {
			return nil, err
		}
	}
	hostRequested := (call.Name == "exec_command" || call.Name == "apply_patch") && args["request_unsandboxed"] == true
	if call.Name == "apply_patch" {
		prepared, _ = json.Marshal(map[string]any{"patch": args["patch"], "cwd": cwd, "request_unsandboxed": hostRequested, "justification": args["justification"]})
	}
	mode, err := d.Permissions.authorize(ctx, HostRequest{Owner: approvalOwner, Task: d.Task, Turn: d.Turn, Generation: d.Generation, CallID: call.ID, MachineID: target.RuntimeID, EnrollmentEpoch: epoch, Tool: call.Name, Args: prepared, Command: commandContext, Host: hostRequested}, func() error { return state("waiting_approval", &target, displayCWD) })
	if err != nil {
		if errors.Is(err, ErrHostDenied) {
			if emitErr := state("denied", &target, displayCWD); emitErr != nil {
				return nil, emitErr
			}
		}
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
	request := executor.Request{Command: commandContext, Mode: mode, Revision: permissionPolicy.Revision, ID: call.ID, Operation: name, CWD: cwd, Policy: d.Spec.Params.Agent.Shell, Args: engineArgs}
	if name == "exec_command" || name == "plugin_call" {
		bindings, refresh, credentialErr := d.credentialBindings(call.Name, args)
		if credentialErr != nil {
			err = credentialErr
		} else if len(bindings) > 0 {
			if d.Credentials == nil {
				err = errors.New("Control credentials are unavailable")
			} else {
				request.Environment = map[string]string{}
				for name, ref := range bindings {
					var value string
					value, err = d.Credentials.Credential(ctx, credentialScope(d.Owner, d.Turn, d.Generation), ref.Service, refresh)
					if err != nil {
						break
					}
					request.Environment[name] = value
				}
			}
		}
	}
	var value json.RawMessage
	if err == nil {
		err = d.Permissions.CheckRevision(target.RuntimeID, epoch, permissionPolicy.Revision)
	}
	if err == nil {
		executionMode = mode
		err = state("running", &target, displayCWD)
	}
	if err != nil {
		if persistErr := d.Operations.Complete(context.WithoutCancel(ctx), key, nil, err); persistErr != nil {
			return nil, errors.Join(core.ErrCleanupUnconfirmed, persistErr)
		}
		return nil, err
	}
	if target.RuntimeID == d.RuntimeID {
		if d.Host == nil {
			err = errors.New("Host executor is unavailable")
		} else {
			var result map[string]any
			executionAttempted = true
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
	if selector != nil {
		return core.ExecutionTarget{}, errors.New("tool execution is fixed to its Runtime")
	}
	return d.DefaultTarget, d.DefaultTarget.Validate()
}
func (d *Dispatcher) prepare(name string, args map[string]any, target core.ExecutionTarget) (string, map[string]any, error) {
	switch name {
	case "exec_command", "write_stdin", "apply_patch":
		if name == "exec_command" || name == "apply_patch" {
			if err := validateHostAccessRequest(args); err != nil {
				return "", nil, err
			}
			copyArgs := make(map[string]any, len(args))
			for key, value := range args {
				if key != "credentials" && key != "refresh_credentials" && key != "request_unsandboxed" && key != "justification" {
					copyArgs[key] = value
				}
			}
			return name, copyArgs, executor.ValidateArguments(name, copyArgs)
		}
		return name, args, executor.ValidateArguments(name, args)
	}
	for _, config := range d.Spec.Params.Agent.Plugins {
		if !config.Enabled || config.Bundle == nil {
			continue
		}
		bundle := config.Bundle
		for _, definition := range bundle.Manifest.Contributes.Tools.Tools {
			var tool struct {
				Name string `json:"name"`
			}
			if json.Unmarshal(definition, &tool) != nil || tool.Name != name {
				continue
			}
			if err := bundle.ValidateInput(name, args); err != nil {
				return "", nil, err
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

func credentialScope(owner, turn string, generation uint64) string {
	hash, _ := core.HashJSON([]any{owner, turn, generation})
	return hash
}
func (d *Dispatcher) credentialBindings(name string, args map[string]any) (core.CredentialBindings, bool, error) {
	bindings := core.CredentialBindings{}
	if args["reset"] == true {
		return bindings, false, nil
	}
	refresh := false
	if value, ok := args["refresh_credentials"]; ok {
		var valid bool
		refresh, valid = value.(bool)
		if !valid {
			return nil, false, errors.New("refresh_credentials must be boolean")
		}
	}
	if name == "exec_command" {
		if value, ok := args["credentials"]; ok {
			raw, err := json.Marshal(value)
			if err != nil {
				return nil, false, err
			}
			if err = core.DecodeJSON(raw, &bindings); err != nil {
				return nil, false, err
			}
		}
	} else {
		for _, plugin := range d.Spec.Params.Agent.Plugins {
			if plugin.Enabled && plugin.Bundle != nil {
				for _, raw := range plugin.Bundle.Manifest.Contributes.Tools.Tools {
					var tool struct {
						Name string `json:"name"`
					}
					if json.Unmarshal(raw, &tool) == nil && tool.Name == name {
						bindings = plugin.Bundle.Manifest.Contributes.Tools.Credentials
					}
				}
			}
		}
	}
	return bindings, refresh, bindings.Validate()
}

func validateHostAccessRequest(args map[string]any) error {
	request := false
	if value, exists := args["request_unsandboxed"]; exists {
		var ok bool
		request, ok = value.(bool)
		if !ok {
			return errors.New("request_unsandboxed must be boolean")
		}
	}
	justification := ""
	if value, exists := args["justification"]; exists {
		var ok bool
		justification, ok = value.(string)
		if !ok {
			return errors.New("justification must be text")
		}
	}
	if request && strings.TrimSpace(justification) == "" {
		return errors.New("request_unsandboxed=true requires a nonempty justification")
	}
	return nil
}
