package control

import (
	"context"
	"encoding/json"
	"github.com/jjyr/pot/pkg/agentlink"
	"testing"
)

func TestLinkedPotRetiresOnlySelectedAssistant(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	a, b := agentControl(t, "delete-source"), agentControl(t, "delete-receiver")
	other, err := b.service.Registry.Create(ctx, "other", "Other")
	if err != nil {
		t.Fatal(err)
	}
	connection := connectAgentControls(t, a, b)
	owner := "agent-link:" + connection.ID + ":main"
	callAgent := func(operation agentlink.Operation, call agentlink.Call) (any, error) {
		return callAgentWhenReady(t, ctx, a.service, owner, operation, call)
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
	// The directory request can be rejected before dispatch while the peer is
	// refreshing its cache. Retry that exact admission rejection only; never
	// repeat deletion or accept a cached result in place of the receiver read.
	value, err = retryAgentCapacity(t, ctx, func(ctx context.Context) (any, error) {
		return a.service.agentLinks.service.Directory(ctx, connection.ID)
	})
	directory, _ := value.(agentlink.Directory)
	if err != nil || len(directory.Assistants()) != 1 || directory.DefaultAssistant() != "" {
		t.Fatal(directory, err)
	}
	local, err := a.service.Registry.Definitions(ctx)
	if err != nil || len(local) != 1 || local[0].ID != "main" {
		t.Fatal("source assistant was changed", local, err)
	}
}
