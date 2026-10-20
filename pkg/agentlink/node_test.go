package agentlink

import (
	"context"
	"errors"
	"github.com/jjyr/pot/pkg/protocol"
	"testing"
	"time"
)

type testBackend struct {
	f         *fixture
	submitted TaskRequest
}

func (b *testBackend) Directory(ctx context.Context) (Directory, error) {
	a, _ := b.f.service.options.Identity(ctx, "assistant")
	other, _ := b.f.service.options.Identity(ctx, "other")
	return Directory{NodeID: b.f.id, Name: b.f.id, Adapter: "pot", Assistants: []Identity{a, other}, DefaultAssistantID: "assistant", Capabilities: []string{"chat", "tasks"}}, nil
}
func (b *testBackend) Submit(ctx context.Context, r TaskRequest) (Task, error) {
	b.submitted = r
	a, _ := b.f.service.options.Identity(ctx, "other")
	return Task{ID: "remote-task", RequestID: r.RequestID, Assistant: a, State: "queued"}, nil
}
func (b *testBackend) Status(context.Context, protocol.AgentRef, string) (Task, error) {
	return Task{ID: "remote-task", State: "complete", Result: "done"}, nil
}
func (b *testBackend) Cancel(context.Context, protocol.AgentRef, string) (Task, error) {
	panic("unsupported cancellation invoked")
}
func TestNodeDirectoryRoutesAllAssistantsAndPreservesDefaultIntent(t *testing.T) {
	ctx := context.Background()
	a, b := newFixture(t, "node-a"), newFixture(t, "node-b")
	ab, bb := &testBackend{f: a}, &testBackend{f: b}
	a.service.options.Backend = ab
	b.service.options.Backend = bb
	c := connect(t, a, b)
	d, e := a.service.Directory(ctx, c.ID)
	if e != nil || len(d.Assistants) != 2 {
		t.Fatal(d, e)
	}
	from, _ := a.service.options.Identity(ctx, "other")
	to, _ := b.service.options.Identity(ctx, "other")
	message, e := a.mail.Send(ctx, "other-route", from.Address, protocol.SendMail{To: to.Address, Text: "hello"}, nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = a.service.Sync(ctx, c.ID); e != nil {
		t.Fatal(e)
	}
	if mailCount(t, b, message.ID) != 1 {
		t.Fatal("node link omitted second assistant")
	}
	task, e := a.service.Submit(ctx, c.ID, TaskRequest{RequestID: "request", From: from.Address, Text: "work"})
	if e != nil || task.ID != "remote-task" || bb.submitted.AssistantID != "" {
		t.Fatal(task, bb.submitted, e)
	}
	if _, e = a.service.Cancel(ctx, c.ID, from.Address, task.ID); !errors.Is(e, ErrUnsupported) {
		t.Fatal(e)
	}
	if e = a.service.Disconnect(ctx, c.ID); e != nil {
		t.Fatal(e)
	}
	if e = a.service.Sync(ctx, c.ID); !errors.Is(e, ErrDisconnected) {
		t.Fatal(e)
	}
	if e = a.service.Reconnect(ctx, c.ID); e != nil {
		t.Fatal(e)
	}
	if e = a.service.Delete(ctx, c.ID); e != nil {
		t.Fatal(e)
	}
	if mailCount(t, b, message.ID) != 1 {
		t.Fatal("deletion removed history")
	}
}

func TestStaleNetworkProjectionCannotResumeDisconnectedConnection(t *testing.T) {
	ctx := context.Background()
	a, b := newFixture(t, "stale-a"), newFixture(t, "stale-b")
	c := connect(t, a, b)
	if e := a.service.Disconnect(ctx, c.ID); e != nil {
		t.Fatal(e)
	}
	if e := a.service.update(ctx, c); e == nil {
		t.Fatal("stale network update overwrote pause")
	}
	current, e := a.service.Get(ctx, c.ID)
	if e != nil || current.State != "disconnected" {
		t.Fatal(current, e)
	}
}

func TestIncomingConnectionReconnectAcceptsResumeWithoutCallbackEndpoint(t *testing.T) {
	ctx := context.Background()
	a, b := newFixture(t, "resume-a"), newFixture(t, "resume-b")
	c := connect(t, a, b)
	if e := b.service.Disconnect(ctx, c.ID); e != nil {
		t.Fatal(e)
	}
	if e := b.service.Reconnect(ctx, c.ID); e != nil {
		t.Fatal("incoming resume requires no callback address", e)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		e := a.service.Sync(ctx, c.ID)
		if e == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(e)
		}
		time.Sleep(time.Millisecond)
	}
}
