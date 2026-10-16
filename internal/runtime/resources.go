package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jjyr/pot/internal/control"
	"github.com/jjyr/pot/internal/core"
	"github.com/jjyr/pot/internal/protocol"
	"github.com/jjyr/pot/internal/sandbox"
)

// ResourceCommand is an advanced local-owner interface. Remote membership
// cannot create definitions or change this Runtime's selected Sandbox.
type ResourceCommand struct {
	Action    string                   `json:"action" required:"true"`
	Kind      string                   `json:"kind"`
	RequestID string                   `json:"request_id"`
	Assistant *control.CreateAssistant `json:"assistant,omitempty"`
	SandboxID string                   `json:"sandbox_id"`
	Name      string                   `json:"name"`
	Provider  string                   `json:"provider"`
	Resources *sandbox.Resources       `json:"resources,omitempty"`
	Mappings  *[]sandbox.HostMapping   `json:"mappings,omitempty"`
	Policy    *control.MachinePolicy   `json:"policy,omitempty"`
}

func (r *Runtime) ResourceCall(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	if method == "GET" {
		return r.ResourceSnapshot(ctx)
	}
	if method != "POST" {
		return nil, errors.New("GET or POST required")
	}
	var request ResourceCommand
	if err := protocol.DecodeStrict(raw, &request); err != nil {
		return nil, err
	}
	digest, err := core.HashJSON(request)
	if err != nil {
		return nil, err
	}
	scope := request.Kind + ":" + request.SandboxID
	if request.Action == "select" {
		scope = "selection"
	}
	if request.Kind == "assistant" {
		scope = "assistant:" + request.RequestID
	}
	if request.Action == "status" {
		return r.applyResource(ctx, request)
	}
	return r.Control.OwnerResourceMutation(ctx, request.RequestID, scope, digest, func() (any, error) { return r.applyResource(ctx, request) })
}
func (r *Runtime) applyResource(ctx context.Context, request ResourceCommand) (any, error) {
	notify := func() { r.Control.Changes.Notify("resources"); r.Control.Changes.Notify("machines") }
	if request.Action != "status" {
		defer notify()
	}
	if request.Action == "create" {
		switch request.Kind {
		case "assistant":
			if request.Assistant != nil {
				return r.Control.CreateAssistant(ctx, *request.Assistant)
			}
			return r.Control.CreateAssistant(ctx, control.CreateAssistant{RequestID: request.RequestID, Name: request.Name})
		case "host":
			policy := control.MachinePolicy{"host", "ask"}
			if request.Policy != nil {
				policy = *request.Policy
			}
			if policy.ExecutionMode != "host" {
				return nil, errors.New("Host creation requires explicit Host policy")
			}
			if err := r.Control.Permissions.Configure(r.Root.Identity.RuntimeID, policy); err != nil {
				return nil, err
			}
			return map[string]any{"kind": "host", "runtime_id": r.Root.Identity.RuntimeID, "policy": policy}, nil
		case "sandbox":
			if r.Sandboxes == nil {
				return nil, errors.New("Sandbox Manager unavailable")
			}
			resources := sandbox.Resources{}
			if request.Resources != nil {
				resources = *request.Resources
			}
			return r.Sandboxes.Create(ctx, request.SandboxID, request.Name, request.Provider, resources)
		default:
			return nil, errors.New("resource kind must be assistant, host or sandbox")
		}
	}
	if request.Kind != "sandbox" || request.SandboxID == "" || r.Sandboxes == nil {
		return nil, errors.New("operation requires an explicit local Sandbox definition")
	}
	switch request.Action {
	case "select":
		if err := r.Sandboxes.Select(ctx, request.SandboxID); err != nil {
			return nil, err
		}
		return map[string]string{"selected_sandbox": request.SandboxID}, nil
	case "update":
		if request.Resources == nil {
			return nil, errors.New("Sandbox resource configuration required")
		}
		err := r.Sandboxes.Configure(ctx, request.SandboxID, *request.Resources)
		return map[string]bool{"updated": err == nil}, err
	case "mappings":
		if request.Mappings == nil {
			return nil, errors.New("explicit Sandbox mappings list required")
		}
		err := r.Sandboxes.ConfigureMappings(ctx, request.SandboxID, *request.Mappings)
		return map[string]bool{"updated": err == nil}, err
	case "recover":
		err := r.Sandboxes.Recover(ctx, request.SandboxID)
		return map[string]bool{"recovered": err == nil}, err
	case "delete":
		selected, err := r.Sandboxes.Selected(ctx)
		if err != nil {
			return nil, err
		}
		if selected == "" {
			selected = sandbox.SharedID
		}
		refs, err := r.Control.SandboxReferences(ctx, request.SandboxID, selected == request.SandboxID)
		if err != nil {
			return nil, err
		}
		if len(refs) > 0 {
			return nil, fmt.Errorf("Sandbox is retained by %v", refs)
		}
		return r.Sandboxes.Lifecycle(ctx, request.SandboxID, "delete")
	case "start", "stop", "status", "rebuild":
		return r.Sandboxes.Lifecycle(ctx, request.SandboxID, request.Action)
	default:
		return nil, errors.New("unknown local resource action")
	}
}
func (r *Runtime) ResourceSnapshot(ctx context.Context) (any, error) {
	definitions, err := r.Control.Registry.Definitions(ctx)
	if err != nil {
		return nil, err
	}
	policy, _, err := r.Control.Permissions.Policy(r.Root.Identity.RuntimeID)
	if err != nil {
		return nil, err
	}
	result := map[string]any{"runtime_id": r.Root.Identity.RuntimeID, "assistants": definitions, "host": map[string]any{"available": true, "policy": policy}, "sandbox_definitions": []sandbox.Definition{}, "sandbox_providers": []sandbox.Descriptor{}, "selected_sandbox": ""}
	if r.Sandboxes != nil {
		inventory, err := r.Sandboxes.Inventory(ctx)
		if err != nil {
			return nil, err
		}
		selected, err := r.Sandboxes.Selected(ctx)
		if err != nil {
			return nil, err
		}
		result["sandbox_definitions"] = inventory
		result["sandbox_providers"] = r.Sandboxes.Descriptors()
		result["selected_sandbox"] = selected
	}
	receipts, err := r.Control.OwnerResourceReceipts(ctx)
	if err != nil {
		return nil, err
	}
	result["commands"] = receipts
	return result, nil
}
