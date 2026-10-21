package agentlink

import (
	"context"
	"errors"
	"github.com/jjyr/pot/pkg/protocol"
	"sync/atomic"
	"testing"
	"time"
)

type testBackend struct {
	f                 *fixture
	submitted         TaskRequest
	observationFailed atomic.Bool
}

func (b *testBackend) ChatStatus(context.Context, ChatLookup) (ChatStatus, error) {
	return ChatStatus{}, ErrUnsupported
}

func (b *testBackend) Observe(ctx context.Context, _ string) (Observation, error) {
	if b.observationFailed.Load() {
		return Observation{}, ErrUnavailable
	}
	d, err := b.Directory(ctx)
	value := Observation{NodeID: d.NodeID, Assistants: []Activity{}}
	for _, a := range d.Assistants {
		value.Assistants = append(value.Assistants, Activity{AssistantID: a.Address.AgentID, ChatState: "idle"})
	}
	return value, err
}

func (b *testBackend) Directory(ctx context.Context) (Directory, error) {
	a, _ := b.f.service.options.Identity(ctx, "assistant")
	other, _ := b.f.service.options.Identity(ctx, "other")
	description := Description{Version: 1, Scope: "peer", Operations: []Operation{ChatRead, ChatSend, ChatReceipt, TaskSubmit, TaskRead}}
	return Directory{NodeID: b.f.id, Name: b.f.id, Adapter: "pot", Assistants: []Identity{a, other}, DefaultAssistantID: "assistant", Operations: map[string]Description{a.Address.AgentID: description, other.Address.AgentID: description}}, nil
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
	return Task{}, ErrUnsupported
}

func TestTaskCallerKindsAndConfigurationAuthorityOnPeerTransport(t *testing.T) {
	ctx := context.Background()
	a, b := newFixture(t, "caller-a"), newFixture(t, "caller-b")
	a.service.options.Backend = &testBackend{f: a}
	backend := &testBackend{f: b}
	b.service.options.Backend = backend
	c := connect(t, a, b)
	for _, kind := range []string{"user", "task", "assistant"} {
		from := protocol.AgentRef{NodeID: a.id, Kind: kind, AgentID: "source"}
		request := TaskRequest{From: from, RequestID: "request-" + kind, Text: "Work"}
		if _, err := a.service.Submit(ctx, c.ID, request); err != nil || backend.submitted.From != from {
			t.Fatal(kind, backend.submitted, err)
		}
	}
	for _, kind := range []string{"node", "runtime"} {
		if _, err := a.service.Submit(ctx, c.ID, TaskRequest{From: protocol.AgentRef{NodeID: a.id, Kind: kind, AgentID: "source"}, RequestID: "forged", Text: "Work"}); !errors.Is(err, ErrUnauthorized) {
			t.Fatal(kind, err)
		}
	}
	session, err := a.service.ensureSession(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	configuration := protocol.CreateTask{RequestID: "owner-config", Text: "Work"}
	input := TaskRequest{From: protocol.AgentRef{NodeID: a.id, Kind: "assistant", AgentID: "source"}, RequestID: configuration.RequestID, Text: configuration.Text, Configuration: &configuration}
	var result Task
	if err = a.service.call(ctx, session, "task.submit", input, &result); !errors.Is(err, ErrUnsupported) {
		t.Fatal("peer transport admitted owner configuration", err)
	}
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
