package control

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/protocol"
	"testing"
	"time"
)

func TestLinkedPotRetiresOnlySelectedAssistant(t *testing.T) {
	ctx := context.Background()
	a, b := agentControl(t, "delete-source"), agentControl(t, "delete-receiver")
	other, err := b.service.Registry.Create(ctx, "other", "Other")
	if err != nil {
		t.Fatal(err)
	}
	connection := connectAgentControls(t, a, b)
	owner := "agent-link:" + connection.ID + ":main"
	callAgent := func(operation agentlink.Operation, call agentlink.Call) (any, error) {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		for {
			value, err := a.service.CallAgent(ctx, owner, operation, call)
			var api protocol.APIError
			if !errors.As(err, &api) || api.Code != agentlink.ErrCapacity.Error() {
				return value, err
			}
			// Background observations share the one-operation admission limit.
			// Capacity rejection happens before execution, so only that outcome
			// can safely retry the exact profile request.
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(20 * time.Millisecond):
			}
		}
	}
	d, err := a.service.AgentCapabilities(ctx, owner)
	if err != nil || !d.Supports(agentlink.AssistantDelete) {
		t.Fatal(d, err)
	}
	value, err := callAgent(agentlink.AssistantProfile, agentlink.Call{Action: "get"})
	if err != nil {
		t.Fatal(err)
	}
	p := value.(agentlink.Profile)
	update, _ := json.Marshal(map[string]any{"name": "Remote name", "revision": p.Revision})
	value, err = callAgent(agentlink.AssistantProfile, agentlink.Call{Action: "write", Body: update})
	if err != nil {
		t.Fatal(err)
	}
	p = value.(agentlink.Profile)
	if p.Name != "Remote name" || p.Avatar == "" {
		t.Fatal("remote profile not saved", p)
	}
	if _, err = callAgent(agentlink.AssistantProfile, agentlink.Call{Action: "read"}); err != nil {
		t.Fatal("default avatar", err)
	}
	if _, err = callAgent(agentlink.AssistantDelete, agentlink.Call{Body: json.RawMessage(`{"revision":999}`)}); err == nil {
		t.Fatal("stale revision accepted")
	}
	body, _ := json.Marshal(map[string]any{"revision": p.Revision})
	if _, err = callAgent(agentlink.AssistantDelete, agentlink.Call{Body: body}); err != nil {
		t.Fatal(err)
	}
	defs, err := b.service.Registry.Definitions(ctx)
	if err != nil || len(defs) != 1 || defs[0].ID != other.ID {
		t.Fatal(defs, err)
	}
	// Deletion remains successful even if its best-effort cache refresh fails.
	// Read the receiver's directory rather than racing cached observations.
	directory, err := a.service.agentLinks.service.Directory(ctx, connection.ID)
	if err != nil || len(directory.Assistants()) != 1 || directory.DefaultAssistant() != "" {
		t.Fatal(directory, err)
	}
	local, err := a.service.Registry.Definitions(ctx)
	if err != nil || len(local) != 1 || local[0].ID != "main" {
		t.Fatal("source assistant was changed", local, err)
	}
}
