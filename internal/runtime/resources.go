package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/internal/control"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

type ResourceCommand struct {
	Action    string                   `json:"action" required:"true"`
	Kind      string                   `json:"kind"`
	RequestID string                   `json:"request_id"`
	Assistant *control.CreateAssistant `json:"assistant,omitempty"`
	Name      string                   `json:"name"`
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
	if request.Action != "create" || request.Kind != "assistant" {
		return nil, errors.New("only assistant creation is supported")
	}
	digest, err := core.HashJSON(request)
	if err != nil {
		return nil, err
	}
	return r.Control.OwnerResourceMutation(ctx, request.RequestID, "assistant:"+request.RequestID, digest, func() (any, error) {
		defer r.Control.Changes.Notify("resources")
		if request.Assistant != nil {
			return r.Control.CreateAssistant(ctx, *request.Assistant)
		}
		return r.Control.CreateAssistant(ctx, control.CreateAssistant{RequestID: request.RequestID, Name: request.Name})
	})
}
func (r *Runtime) ResourceSnapshot(ctx context.Context) (any, error) {
	definitions, err := r.Control.Registry.Definitions(ctx)
	if err != nil {
		return nil, err
	}
	receipts, err := r.Control.OwnerResourceReceipts(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{"runtime_id": r.Root.Identity.RuntimeID, "assistants": definitions, "commands": receipts}, nil
}
