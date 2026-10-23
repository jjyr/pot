package control

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"sync"
	"testing"

	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/peer"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

// An independent Adapter fixture intentionally has no assistant directory.
// The product must discover task execution from capabilities, not its brand.
type fixtureTaskExecutor struct {
	node, adapter string
	mu            sync.Mutex
	requests      map[string]agentlink.TaskRequest
	tasks         map[string]agentlink.Task
}

func (b *fixtureTaskExecutor) Directory(context.Context) (agentlink.Directory, error) {
	return agentlink.Directory{NodeID: b.node, Name: "Fixture Agent", Adapter: b.adapter, Targets: []agentlink.Endpoint{{Identity: agentlink.Identity{Address: agentlink.NodeTarget(b.node), Name: "Fixture Agent", Adapter: b.adapter}, Capabilities: agentlink.Description{Version: 1, Scope: "caller", Operations: []agentlink.Operation{agentlink.TaskSubmit, agentlink.TaskRead, agentlink.TaskCancel, agentlink.TaskInput}}}}}, nil
}
func (b *fixtureTaskExecutor) Observe(context.Context, string) (agentlink.Observation, error) {
	return agentlink.Observation{NodeID: b.node, Executor: &agentlink.Activity{ChatState: "idle"}}, nil
}
func (b *fixtureTaskExecutor) Submit(_ context.Context, r agentlink.TaskRequest) (agentlink.Task, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if r.Target != agentlink.NodeTarget(b.node) {
		return agentlink.Task{}, agentlink.ErrUnsupported
	}
	if old, ok := b.requests[r.RequestID]; ok {
		if old.From != r.From || old.Text != r.Text {
			return agentlink.Task{}, agentlink.ErrConflict
		}
		return b.tasks[r.RequestID], nil
	}
	identity := agentlink.Identity{Address: protocol.AgentRef{NodeID: b.node, Kind: "task", AgentID: r.RequestID}, Name: "Fixture Agent", Adapter: b.adapter}
	task := agentlink.Task{ID: r.RequestID, RequestID: r.RequestID, RunID: "run-" + r.RequestID, Receiver: identity, State: "completed", Result: r.Text}
	b.requests[r.RequestID], b.tasks[r.RequestID] = r, task
	return task, nil
}
func (b *fixtureTaskExecutor) Status(_ context.Context, from protocol.AgentRef, id string) (agentlink.Task, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if r, ok := b.requests[id]; !ok || r.From != from {
		return agentlink.Task{}, agentlink.ErrNotFound
	}
	return b.tasks[id], nil
}
func (b *fixtureTaskExecutor) Cancel(ctx context.Context, from protocol.AgentRef, id string) (agentlink.Task, error) {
	return b.Status(ctx, from, id)
}
func (b *fixtureTaskExecutor) Input(ctx context.Context, r agentlink.TaskGuidance) (agentlink.Task, error) {
	if _, err := b.Status(ctx, r.From, r.ID); err != nil {
		return agentlink.Task{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	task := b.tasks[r.ID]
	task.RunID = r.RequestID
	task.Result = r.Text
	b.tasks[r.ID] = task
	return task, nil
}

func connectFixtureExecutor(t *testing.T, source *Service, node, adapter string) agentlink.Connection {
	t.Helper()
	receiver := agentControl(t, node).service
	db, err := storage.OpenDatabase(filepath.Join(t.TempDir(), "control.sqlite"), storage.ControlDatabase)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	host, key := receiver.Machines.TransportIdentity()
	backend := &fixtureTaskExecutor{node: node, adapter: adapter, requests: map[string]agentlink.TaskRequest{}, tasks: map[string]agentlink.Task{}}
	service, err := agentlink.New(agentlink.Options{DB: db, Secrets: receiver.integrationSecrets, Mailbox: controlLinkMailbox{receiver}, HostIdentity: host, HostKey: key, Backend: backend, Identity: func(context.Context, string) (agentlink.Identity, error) {
		return agentlink.Identity{Address: protocol.AgentRef{NodeID: node, Kind: "node", AgentID: node}, Name: "Fixture Agent", Adapter: adapter}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	server := peer.Server{Identity: host, PrivateKey: key, Authorize: service.Authorize, OnSession: service.Attach}
	go func() { defer close(done); _ = server.Serve(ctx, listener) }()
	t.Cleanup(func() { cancel(); listener.Close(); <-done })
	invitation, err := service.Invite(ctx, "tcp://"+listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	connection, err := source.agentLinks.service.Connect(ctx, invitation.Address)
	if err != nil {
		t.Fatal(err)
	}
	return connection
}

func TestTaskExecutorSelectionUsesCapabilitiesAcrossAdapters(t *testing.T) {
	source := agentControl(t, "source").service
	ctx := context.Background()
	first := connectFixtureExecutor(t, source, "first", "fixture-agent")
	request := protocol.CreateTask{Target: &protocol.AgentRef{NodeID: "first", Kind: "node", AgentID: "first"}, RequestID: "generic-task", Text: "Generic task\nExecute through the advertised capability"}
	accepted, err := source.CreateTask(ctx, "main", request)
	if err != nil {
		t.Fatal(err)
	}
	source.syncTaskExecutions(ctx)
	record, err := source.Tasks.Get(ctx, "main", accepted.ID)
	if err != nil || record.Info.Phase != protocol.TaskComplete || record.Info.AgentLink.LinkID != first.ID || record.Execution.Receipt.Receiver.Adapter != "fixture-agent" {
		t.Fatal(record, err)
	}
	guidance := protocol.TaskInput{RequestID: "next-run", Text: "Continue"}
	if _, err = source.guideTaskExecution(ctx, "main", accepted.ID, guidance); err != nil {
		t.Fatal(err)
	}
	record, err = source.Tasks.Get(ctx, "main", accepted.ID)
	if err != nil || record.Execution.Receipt.RunID != "next-run" || record.Result.Text != "Continue" {
		t.Fatal(record, err)
	}
	second := connectFixtureExecutor(t, source, "second", "another-agent")
	request.RequestID = "second-task"
	request.Target = &protocol.AgentRef{NodeID: "second", Kind: "node", AgentID: "second"}
	if _, err = source.CreateTask(ctx, "main", request); err != nil {
		t.Fatal(err)
	}
	// Accepted retries remain bound to the first receiver after directory changes.
	request.Target = &protocol.AgentRef{NodeID: "first", Kind: "node", AgentID: "first"}
	request.RequestID = "generic-task"
	if retry, err := source.CreateTask(ctx, "main", request); err != nil || retry.ID != accepted.ID {
		t.Fatal(retry, err)
	}
	if err = source.agentLinks.service.Disconnect(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	request.RequestID = "disconnected-target"
	if _, err = source.CreateTask(ctx, "main", request); !errors.Is(err, agentlink.ErrDisconnected) {
		t.Fatal("retargeted disconnected work", err)
	}
	request.Target = &protocol.AgentRef{NodeID: "second", Kind: "node", AgentID: "second"}
	if task, err := source.CreateTask(ctx, "main", request); err != nil || task.AgentLink.LinkID != second.ID {
		t.Fatal(task, err)
	}
	request.Target = &protocol.AgentRef{NodeID: "second", Kind: "assistant", AgentID: "absent"}
	request.RequestID = "no-assistant"
	if _, err = source.CreateTask(ctx, "main", request); !errors.Is(err, agentlink.ErrUnsupported) {
		t.Fatal(err)
	}
	var executionCount int
	if err = source.Tasks.DB().QueryRow("SELECT count(*) FROM tasks WHERE json_extract(body,'$.execution') IS NOT NULL").Scan(&executionCount); err != nil || executionCount != 3 {
		t.Fatal(executionCount, err)
	}
}
