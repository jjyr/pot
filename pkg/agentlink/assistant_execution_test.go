package agentlink

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/jjyr/pot/pkg/protocol"
)

type managementBackend struct {
	*testBackend
	enabled atomic.Bool
	calls   atomic.Int32
	corrupt atomic.Bool
}

func (b *managementBackend) Directory(ctx context.Context) (Directory, error) {
	d, e := b.testBackend.Directory(ctx)
	if b.enabled.Load() {
		d.Targets = append(d.Targets, Endpoint{Identity: Identity{Address: NodeTarget(d.NodeID), Name: "Host", Adapter: d.Adapter}, Capabilities: Description{Version: 1, Scope: "caller", Operations: []Operation{AssistantManagement}}})
	}
	return d, e
}
func (b *managementBackend) ManageAssistant(_ context.Context, input AssistantManagementRequest) (AssistantManagementResult, error) {
	b.calls.Add(1)
	if input.Action != "catalog" {
		node := b.f.id
		if b.corrupt.Load() {
			node = "other-node"
		}
		return AssistantManagementResult{Assistant: &ManagedAssistant{ID: input.ID, Address: AssistantTarget(node, input.ID), Revision: 1}}, nil
	}
	return AssistantManagementResult{Models: []ExecutionModel{{ID: "model", Name: "Model"}}}, nil
}
func TestAssistantManagementRequiresAdvertisedCapabilityAndAuthenticatedUser(t *testing.T) {
	ctx := context.Background()
	a, b := newFixture(t, "manage-a"), newFixture(t, "manage-b")
	a.service.options.Backend = &testBackend{f: a}
	receiver := &managementBackend{testBackend: &testBackend{f: b}}
	b.service.options.Backend = receiver
	c := connect(t, a, b)
	input := AssistantManagementRequest{From: protocol.AgentRef{NodeID: a.id, Kind: "user", AgentID: "operator"}, Action: "catalog"}
	if _, e := a.service.ManageAssistant(ctx, c.ID, input); !errors.Is(e, ErrUnsupported) {
		t.Fatal("unadvertised management reached backend", e)
	}
	receiver.enabled.Store(true)
	session, e := a.service.ensureSession(ctx, c)
	if e != nil {
		t.Fatal(e)
	}
	for _, from := range []protocol.AgentRef{{NodeID: a.id, Kind: "assistant", AgentID: "agent"}, {NodeID: "forged", Kind: "user", AgentID: "operator"}, {NodeID: a.id, Kind: "task", AgentID: "task"}} {
		forged := input
		forged.From = from
		var result AssistantManagementResult
		if e = a.service.call(ctx, session, string(AssistantManagement), forged, &result); e == nil {
			t.Fatal("forged caller reached management", from)
		}
	}
	if receiver.calls.Load() != 0 {
		t.Fatal("unauthorized management executed")
	}
	result, e := a.service.ManageAssistant(ctx, c.ID, input)
	if e != nil || len(result.Models) != 1 || receiver.calls.Load() != 1 {
		t.Fatal(result, e, receiver.calls.Load())
	}
	receiver.corrupt.Store(true)
	mutation := input
	mutation.Action = "create"
	mutation.ID = "same-name"
	mutation.RequestID = "create"
	if _, e = a.service.ManageAssistant(ctx, c.ID, mutation); !errors.Is(e, ErrConflict) {
		t.Fatal("accepted another node's identity", e)
	}
	receiver.corrupt.Store(false)
	if value, e := a.service.ManageAssistant(ctx, c.ID, mutation); e != nil || value.Assistant.Address != AssistantTarget(b.id, "same-name") {
		t.Fatal(value, e)
	}
	if e = a.service.Disconnect(ctx, c.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = a.service.ManageAssistant(ctx, c.ID, input); e == nil {
		t.Fatal("disconnected management executed")
	}
}
