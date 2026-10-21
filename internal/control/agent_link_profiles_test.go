package control

import (
	"context"
	"encoding/json"
	"github.com/jjyr/pot/pkg/agentlink"
	"testing"
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
	d, err := a.service.AgentCapabilities(ctx, owner)
	if err != nil || !d.Supports(agentlink.AssistantDelete) {
		t.Fatal(d, err)
	}
	value, err := a.service.CallAgent(ctx, owner, agentlink.AssistantProfile, agentlink.Call{Action: "get"})
	if err != nil {
		t.Fatal(err)
	}
	p := value.(agentlink.Profile)
	update, _ := json.Marshal(map[string]any{"name": "Remote name", "revision": p.Revision})
	value, err = a.service.CallAgent(ctx, owner, agentlink.AssistantProfile, agentlink.Call{Action: "write", Body: update})
	if err != nil {
		t.Fatal(err)
	}
	p = value.(agentlink.Profile)
	if p.Name != "Remote name" || p.Avatar == "" {
		t.Fatal("remote profile not saved", p)
	}
	if _, err = a.service.CallAgent(ctx, owner, agentlink.AssistantProfile, agentlink.Call{Action: "read"}); err != nil {
		t.Fatal("default avatar", err)
	}
	if _, err = a.service.CallAgent(ctx, owner, agentlink.AssistantDelete, agentlink.Call{Body: json.RawMessage(`{"revision":999}`)}); err == nil {
		t.Fatal("stale revision accepted")
	}
	body, _ := json.Marshal(map[string]any{"revision": p.Revision})
	if _, err = a.service.CallAgent(ctx, owner, agentlink.AssistantDelete, agentlink.Call{Body: body}); err != nil {
		t.Fatal(err)
	}
	defs, err := b.service.Registry.Definitions(ctx)
	if err != nil || len(defs) != 1 || defs[0].ID != other.ID {
		t.Fatal(defs, err)
	}
	link, err := a.service.agentLinks.service.Get(ctx, connection.ID)
	if err != nil || len(link.Directory.Assistants) != 1 || link.Directory.DefaultAssistantID != "" {
		t.Fatal(link, err)
	}
	local, err := a.service.Registry.Definitions(ctx)
	if err != nil || len(local) != 1 || local[0].ID != "main" {
		t.Fatal("source assistant was changed", local, err)
	}
}
