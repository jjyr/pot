package agentlink

import (
	"context"
	"errors"
	"github.com/jjyr/pot/pkg/protocol"
	"sync/atomic"
	"testing"
)

type deletionBackend struct {
	*testBackend
	advertise atomic.Bool
	calls     atomic.Int32
}

func (b *deletionBackend) Directory(ctx context.Context) (Directory, error) {
	d, err := b.testBackend.Directory(ctx)
	if b.advertise.Load() {
		for id, description := range d.Operations {
			description.Operations = append(description.Operations, AssistantDelete)
			d.Operations[id] = description
		}
	}
	return d, err
}
func (b *deletionBackend) DeleteAssistant(context.Context, DeleteAssistantRequest) error {
	b.calls.Add(1)
	return nil
}

func TestPeerDeletionRequiresCapabilityAndAuthenticatedUser(t *testing.T) {
	ctx := context.Background()
	a, b := newFixture(t, "delete-a"), newFixture(t, "delete-b")
	a.service.options.Backend = &testBackend{f: a}
	receiver := &deletionBackend{testBackend: &testBackend{f: b}}
	b.service.options.Backend = receiver
	c := connect(t, a, b)
	input := DeleteAssistantRequest{From: protocol.AgentRef{NodeID: a.id, Kind: "user", AgentID: "user"}, AssistantID: "assistant", Revision: 1}
	if _, err := a.service.DeleteAssistant(ctx, c.ID, input); !errors.Is(err, ErrUnsupported) {
		t.Fatal("unadvertised deletion", err)
	}
	receiver.advertise.Store(true)
	session, err := a.service.ensureSession(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	for _, from := range []protocol.AgentRef{{NodeID: a.id, Kind: "assistant", AgentID: "agent"}, {NodeID: "forged", Kind: "user", AgentID: "user"}} {
		forged := input
		forged.From = from
		var result AssistantDeletion
		if err := a.service.call(ctx, session, "assistant.delete", forged, &result); err == nil {
			t.Fatal("forged caller deleted", from)
		}
	}
	if receiver.calls.Load() != 0 {
		t.Fatal("unauthorized caller reached backend")
	}
	if _, err = a.service.DeleteAssistant(ctx, c.ID, input); err != nil || receiver.calls.Load() != 1 {
		t.Fatal(err, receiver.calls.Load())
	}
	if err = a.service.Disconnect(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = a.service.DeleteAssistant(ctx, c.ID, input); err == nil || receiver.calls.Load() != 1 {
		t.Fatal("offline deletion reached receiver", err)
	}
}
