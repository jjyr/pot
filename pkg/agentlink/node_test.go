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

func (b *testBackend) Observe(ctx context.Context, _ string) (Observation, error) {
	if b.observationFailed.Load() {
		return Observation{}, ErrUnavailable
	}
	d, err := b.Directory(ctx)
	value := Observation{NodeID: d.NodeID, Assistants: []Activity{}}
	for _, a := range d.Assistants() {
		value.Assistants = append(value.Assistants, Activity{AssistantID: a.Address.AgentID, ChatState: "idle"})
	}
	return value, err
}

func (b *testBackend) Directory(ctx context.Context) (Directory, error) {
	a, _ := b.f.service.options.Identity(ctx, "assistant")
	other, _ := b.f.service.options.Identity(ctx, "other")
	description := Description{Version: 1, Scope: "peer", Operations: []Operation{ChatRead, ChatSend, ChatReceipt, TaskSubmit, TaskRead}}
	ref := a.Address
	return Directory{NodeID: b.f.id, Name: b.f.id, Adapter: "pot", Targets: []Endpoint{{Identity: a, Capabilities: description}, {Identity: other, Capabilities: description}}, DefaultTarget: &ref}, nil
}
func (b *testBackend) Submit(ctx context.Context, r TaskRequest) (Task, error) {
	b.submitted = r
	a, _ := b.f.service.options.Identity(ctx, "other")
	return Task{ID: "remote-task", RequestID: r.RequestID, Receiver: a, State: "queued"}, nil
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
		request := TaskRequest{From: from, RequestID: "request-" + kind, Text: "Work", Target: AssistantTarget(b.id, "other")}
		if _, err := a.service.Submit(ctx, c.ID, request); err != nil || backend.submitted.From != from {
			t.Fatal(kind, backend.submitted, err)
		}
	}
	for _, kind := range []string{"node", "runtime"} {
		if _, err := a.service.Submit(ctx, c.ID, TaskRequest{From: protocol.AgentRef{NodeID: a.id, Kind: kind, AgentID: "source"}, RequestID: "forged", Text: "Work", Target: AssistantTarget(b.id, "other")}); !errors.Is(err, ErrUnauthorized) {
			t.Fatal(kind, err)
		}
	}
	for _, target := range []protocol.AgentRef{{NodeID: b.id, Kind: "runtime", AgentID: "runtime"}, {NodeID: b.id, Kind: "node", AgentID: "wrong"}} {
		request := TaskRequest{From: protocol.AgentRef{NodeID: a.id, Kind: "assistant", AgentID: "source"}, RequestID: "invalid-target", Text: "Work", Target: target}
		if _, err := a.service.Submit(ctx, c.ID, request); !errors.Is(err, ErrInvalid) {
			t.Fatal(target, err)
		}
	}
	session, err := a.service.ensureSession(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	var invalidResult Task
	invalidRequest := TaskRequest{From: protocol.AgentRef{NodeID: a.id, Kind: "assistant", AgentID: "source"}, RequestID: "invalid-wire", Text: "Work", Target: AssistantTarget("foreign-node", "other")}
	if err = a.service.call(ctx, session, "task.submit", invalidRequest, &invalidResult); !errors.Is(err, ErrInvalid) {
		t.Fatal("wire selector bypassed validation", err)
	}
	configuration := protocol.CreateTask{RequestID: "owner-config", Text: "Work"}
	input := TaskRequest{From: protocol.AgentRef{NodeID: a.id, Kind: "assistant", AgentID: "source"}, RequestID: configuration.RequestID, Text: configuration.Text, Configuration: &configuration, Target: AssistantTarget(b.id, "other")}
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
	if e != nil || len(d.Assistants()) != 2 {
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
	task, e := a.service.Submit(ctx, c.ID, TaskRequest{RequestID: "request", From: from.Address, Text: "work", Target: AssistantTarget(b.id, "other")})
	if e != nil || task.ID != "remote-task" || bb.submitted.Target != AssistantTarget(b.id, "other") {
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

type guidanceBackend struct {
	*testBackend
	supportsInput atomic.Bool
	inputs        atomic.Int64
}

func (b *guidanceBackend) Directory(ctx context.Context) (Directory, error) {
	d, err := b.testBackend.Directory(ctx)
	d.Targets = append(d.Targets, Endpoint{Identity: Identity{Address: NodeTarget(b.f.id), Name: "Node", Adapter: "pot"}, Capabilities: Description{Version: 1, Scope: "caller", Operations: []Operation{TaskSubmit, TaskInput}}})
	if b.supportsInput.Load() {
		for i := range d.Targets {
			if d.Targets[i].Address.AgentID == "other" {
				d.Targets[i].Capabilities.Operations = append(d.Targets[i].Capabilities.Operations, TaskInput)
			}
		}
	}
	return d, err
}
func (b *guidanceBackend) Status(ctx context.Context, from protocol.AgentRef, id string) (Task, error) {
	assistant, _ := b.f.service.options.Identity(ctx, "other")
	return Task{ID: id, Receiver: assistant, State: "complete"}, nil
}
func (b *guidanceBackend) Input(ctx context.Context, r TaskGuidance) (Task, error) {
	b.inputs.Add(1)
	assistant, _ := b.f.service.options.Identity(ctx, "other")
	return Task{ID: r.ID, RequestID: "original", Receiver: assistant, State: "running"}, nil
}
func TestTaskInputUsesSameCapabilitiesForLocalAndPeerAdapters(t *testing.T) {
	ctx := context.Background()
	a, b := newFixture(t, "input-a"), newFixture(t, "input-b")
	local := &guidanceBackend{testBackend: &testBackend{f: a}}
	remote := &guidanceBackend{testBackend: &testBackend{f: b}}
	a.service.options.Backend, b.service.options.Backend = local, remote
	connection := connect(t, a, b)
	guidance := TaskGuidance{From: protocol.AgentRef{NodeID: a.id, Kind: "assistant", AgentID: "assistant"}, ID: "original-task", RequestID: "guidance", Text: "Continue this task"}
	for _, link := range []string{"", connection.ID} {
		backend := local
		if link != "" {
			backend = remote
		}
		if _, err := a.service.Input(ctx, link, guidance); !errors.Is(err, ErrUnsupported) || backend.inputs.Load() != 0 {
			t.Fatal("unadvertised input dispatched", link, backend.inputs.Load(), err)
		}
		backend.supportsInput.Store(true)
		result, err := a.service.Input(ctx, link, guidance)
		if err != nil || result.ID != guidance.ID || result.State != "running" || backend.inputs.Load() != 1 {
			t.Fatal(link, result, backend.inputs.Load(), err)
		}
		forged := guidance
		forged.From.NodeID = b.id
		if _, err := a.service.Input(ctx, link, forged); !errors.Is(err, ErrInvalid) || backend.inputs.Load() != 1 {
			t.Fatal("foreign local caller admitted", link, err)
		}
	}
}

func TestGenericAdapterValidationRetainsNodeAndReceiptBindings(t *testing.T) {
	identity := Identity{Address: protocol.AgentRef{NodeID: "receiver", Kind: "node", AgentID: "receiver"}, Adapter: "fixture-agent", Name: "Agent"}
	if !validNodeIdentity(identity) {
		t.Fatal(identity)
	}
	for _, adapter := range []string{"", "..", "../agent", "agent/type", string(make([]byte, 129))} {
		invalid := identity
		invalid.Adapter = adapter
		if validIdentity(invalid) {
			t.Fatal("invalid adapter identity accepted", adapter)
		}
		if err := validateDirectory(Directory{NodeID: "receiver", Name: "Agent", Adapter: adapter}, "receiver"); err == nil {
			t.Fatal("invalid adapter directory accepted", adapter)
		}
	}
	directory := Directory{NodeID: "receiver", Name: "Agent", Adapter: "fixture-agent", Targets: []Endpoint{{Identity: identity, Capabilities: Description{Version: 1, Scope: "caller", Operations: []Operation{TaskSubmit, TaskRead}}}}}
	if err := validateDirectory(directory, "receiver"); err != nil {
		t.Fatal(err)
	}
	if err := validateDirectory(directory, "other-node"); err == nil {
		t.Fatal("foreign directory accepted")
	}
	identity.Address = protocol.AgentRef{NodeID: "receiver", Kind: "task", AgentID: "task"}
	receipt := Task{ID: "task", RequestID: "request", Receiver: identity, State: "queued"}
	if err := validateTask(receipt, "receiver", "fixture-agent", "task", "request"); err != nil {
		t.Fatal(err)
	}
	for _, binding := range [][4]string{{"wrong-node", "fixture-agent", "task", "request"}, {"receiver", "other-agent", "task", "request"}, {"receiver", "fixture-agent", "other-task", "request"}, {"receiver", "fixture-agent", "task", "other-request"}} {
		if err := validateTask(receipt, binding[0], binding[1], binding[2], binding[3]); err == nil {
			t.Fatal("receipt binding ignored", binding)
		}
	}
	receipt.Receiver.Address.AgentID = "different-task"
	if err := validateTask(receipt, "receiver", "fixture-agent", "task", "request"); err == nil {
		t.Fatal("execution identity mismatch accepted")
	}
}
