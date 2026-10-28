package control

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/mcpreceiver"
	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/internal/tasks"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/execution"
	"github.com/jjyr/pot/pkg/protocol"
)

type potMCPFakeExecutor struct {
	mu       sync.Mutex
	runs     map[string]tasks.Run
	launches int
}

func (p *potMCPFakeExecutor) Launch(_ context.Context, l tasks.Launch) (tasks.Run, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.launches++
	digest, _ := l.Digest()
	r := tasks.Run{SchemaVersion: tasks.SchemaVersion, Launch: l, LaunchDigest: digest, BootID: "fake", Phase: tasks.Running, StartedAtMS: time.Now().UnixMilli()}
	p.runs[l.RunID] = r
	return r, nil
}
func (p *potMCPFakeExecutor) Inspect(_ context.Context, node, task, run string) (*tasks.Run, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	r, ok := p.runs[run]
	if !ok {
		return nil, nil
	}
	if r.Launch.ControlID != node || r.Launch.TaskID != task {
		return nil, agentlink.ErrUnauthorized
	}
	return &r, nil
}
func (p *potMCPFakeExecutor) Stop(_ context.Context, node, task, run, mode string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	r, ok := p.runs[run]
	if !ok || r.Launch.ControlID != node || r.Launch.TaskID != task {
		return agentlink.ErrFenced
	}
	r.Phase = tasks.Cancelled
	r.CleanupConfirmed = true
	r.Result = &tasks.Terminal{Outcome: core.Cancelled}
	r.FinishedAtMS = time.Now().UnixMilli()
	p.runs[run] = r
	return nil
}
func (p *potMCPFakeExecutor) Resume(context.Context, string, string, string) error {
	return agentlink.ErrUnsupported
}
func (p *potMCPFakeExecutor) complete(run string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	r := p.runs[run]
	r.Phase = tasks.Complete
	r.Result = &tasks.Terminal{Outcome: core.Success, Text: "safe fake result"}
	r.CleanupConfirmed = true
	r.FinishedAtMS = time.Now().UnixMilli()
	p.runs[run] = r
}

func potMCPControl(t *testing.T, node string) (*Service, *potMCPFakeExecutor) {
	t.Helper()
	manager, e := execution.New(node, "fake-boot", execution.DefaultLimits())
	if e != nil {
		t.Fatal(e)
	}
	key, e := secrets.NewMemoryKey(make([]byte, 32))
	if e != nil {
		t.Fatal(e)
	}
	paused := make(chan struct{})
	var initialized sync.Once
	s, e := Open(Options{Root: t.TempDir(), RuntimeID: node, BootID: "fake-boot", Manager: manager, Keys: key, Maintenance: func() bool { initialized.Do(func() { close(paused) }); return true }})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	// The fixture replaces only the borrowed Task adapter after the real loop
	// has subscribed and entered maintenance; no running loop reads this pointer.
	select {
	case <-paused:
	case <-time.After(2 * time.Second):
		t.Fatal("task loop did not pause")
	}
	if _, e = legacyMainAssistant(t.Context(), s, "main", "Main"); e != nil {
		t.Fatal(e)
	}
	settings := core.DefaultAgentSettings()
	settings.BuiltinTools = []string{}
	settings.Plugins = map[string]core.PluginConfig{}
	profile := core.Profile{ID: "fake", Revision: 1, Name: "Fake", Provider: "fake", Model: "fake", AuthRef: "provider:fake", Params: core.Parameters{MaxOutputTokens: 100, Agent: settings}, ToolPolicy: core.ToolPolicy{SummaryReadOnly: true, Unattended: "runtime"}}
	config, e := profile.Resolve()
	if e != nil {
		t.Fatal(e)
	}
	executor := &potMCPFakeExecutor{runs: map[string]tasks.Run{}}
	s.Tasks, e = tasks.Open(s.Tasks.DB(), tasks.Options{NodeID: node, Executor: executor, ResolveModel: func(context.Context, string, protocol.CreateTask) (tasks.Selection, error) {
		return tasks.Selection{Config: config, Plugins: []string{}}, nil
	}})
	if e != nil {
		t.Fatal(e)
	}
	return s, executor
}
func potMCPServer(t *testing.T, s *Service) *mcpAccessFixture {
	t.Helper()
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	access, e := s.NewMCPAccess(MCPOAuthOptions{Origin: server.URL, LoginURL: "/mcp-connect.html", OwnerSession: func(r *http.Request) (string, bool) {
		return r.Header.Get("X-Fixture-Owner"), r.Header.Get("X-Fixture-Owner") != ""
	}}, func(_ context.Context, _ agentlink.MCPReceiver, target protocol.AgentRef) (agentlink.Description, error) {
		if target.Kind == "node" {
			return PotMCPNodeCapabilities(), nil
		}
		if target.Kind != "assistant" {
			return agentlink.Description{}, agentlink.ErrUnsupported
		}
		return PotMCPReceiverCapabilities(), nil
	})
	if e != nil {
		t.Fatal(e)
	}
	mux.Handle("/", access)
	t.Cleanup(access.Close)
	return &mcpAccessFixture{access: access, oauth: access.options.OAuth, queue: access.options.Queue, http: server}
}
func potMCPAuthorize(t *testing.T, s *Service, f *mcpAccessFixture, existing string) PotMCPConnection {
	t.Helper()
	ctx := t.Context()
	authorization, e := s.BeginPotMCPConnection(ctx, f.http.URL+"/mcp", "http://127.0.0.1:9876/mcp-pot-callback.html", existing)
	if e != nil {
		t.Fatal(e)
	}
	u, e := url.Parse(authorization.AuthorizationURL)
	if e != nil {
		t.Fatal(e)
	}
	response := f.request(t, "GET", u.RequestURI(), "synthetic-owner", nil)
	if response.StatusCode != 303 {
		t.Fatal(response.StatusCode)
	}
	consent, e := url.Parse(response.Header.Get("Location"))
	response.Body.Close()
	if e != nil {
		t.Fatal(e)
	}
	var decision struct {
		Redirect string `json:"redirect"`
	}
	fixtureResponse(t, f.request(t, "POST", "/oauth/consent", "synthetic-owner", mcpConsentInput{RequestID: consent.Query().Get("request"), Approve: true}), 200, &decision)
	callback, e := url.Parse(decision.Redirect)
	if e != nil {
		t.Fatal(e)
	}
	connection, e := s.CompletePotMCPConnection(ctx, callback.Query().Get("state"), callback.Query().Get("code"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.CompletePotMCPConnection(ctx, callback.Query().Get("state"), callback.Query().Get("code")); !errors.Is(e, agentlink.ErrNotFound) {
		t.Fatal("OAuth state replay", e)
	}
	return connection
}
func potMCPClient(t *testing.T, s *Service, id string) *mcpreceiver.Client {
	t.Helper()
	c, token, e := s.potMCPGrant(t.Context(), id)
	if e != nil {
		t.Fatal(e)
	}
	identity, e := agentlink.NewMCPReceiver(id, s.options.RuntimeID)
	if e != nil {
		t.Fatal(e)
	}
	client, e := mcpreceiver.Connect(t.Context(), c.URL, token.AccessToken, identity)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(client.Close)
	registrations, e := s.potMCPRegistrations(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	for _, r := range registrations {
		if e = client.Register(t.Context(), r); e != nil {
			t.Fatal(e)
		}
	}
	if e = s.syncPotMCPDirectory(t.Context(), id, client); e != nil {
		t.Fatal(e)
	}
	return client
}
func potMCPStep(t *testing.T, client *mcpreceiver.Client, p *potMCPReceiver) {
	t.Helper()
	if e := mcpreceiver.Step(t.Context(), client, p); e != nil {
		t.Fatal(e)
	}
}

// One endpoint is reachable: B has no MCP HTTP server. Both are real isolated
// Control/Task/Mailbox services; execution is a harmless fake LocalExecutor.
func TestPotMCPReceiverTaskLifecycleOneReachableEndpoint(t *testing.T) {
	a, _ := potMCPControl(t, "pot-a")
	b, driver := potMCPControl(t, "pot-b")
	server := potMCPServer(t, a)
	connection := potMCPAuthorize(t, b, server, "")
	client := potMCPClient(t, b, connection.ID)
	identity, _ := agentlink.NewMCPReceiver(connection.ID, b.options.RuntimeID)
	receiver := &potMCPReceiver{s: b, identity: identity}
	grant := server.grant(t, false)
	session := server.session(t, grant)
	input := agentlink.MCPSubmitInput{RequestID: "work", Target: agentlink.AssistantTarget("pot-b", "main"), Text: "safe fake work"}
	var accepted MCPTaskAcceptance
	fixtureCall(t, session, "pot_task_submit", input, &accepted)
	potMCPStep(t, client, receiver) // durable claim only
	records, e := receiver.Records(t.Context())
	if e != nil || len(records) != 1 || records[0].Admission != nil {
		t.Fatal(records, e)
	}
	potMCPStep(t, client, receiver) // ack + real local admission + queued event
	records, e = receiver.Records(t.Context())
	if e != nil || records[0].Admission == nil {
		t.Fatal(records, e)
	}
	record := records[0]
	if record.Admission.Binding.Task.NodeID != "pot-b" || record.Admission.Binding.Task.AgentID == accepted.Task.ID || record.Admission.Binding.RunID == "" {
		t.Fatal("lost real mapping", record)
	}
	if e = b.Tasks.Tick(t.Context()); e != nil {
		t.Fatal(e)
	}
	potMCPStep(t, client, receiver)
	var running agentlink.Task
	fixtureCall(t, session, "pot_task_read", map[string]string{"task_id": accepted.Task.ID}, &running)
	if running.RunID != record.Admission.Binding.RunID || running.State != "running" {
		t.Fatal("progress", running)
	}
	driver.complete(running.RunID)
	if e = b.Tasks.Tick(t.Context()); e != nil {
		t.Fatal(e)
	}
	potMCPStep(t, client, receiver)
	var completed agentlink.Task
	fixtureCall(t, session, "pot_task_read", map[string]string{"task_id": accepted.Task.ID}, &completed)
	if completed.State != "complete" || completed.Result != "safe fake result" {
		t.Fatal("terminal", completed)
	}

	// Duplicate durable delivery and event reconciliation never launch work again.
	current, e := receiver.Records(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	if e = receiver.Accept(t.Context(), agentlink.MCPReceiverDelivery{Claim: &current[0].Claim, Work: current[0].Work}); e != nil {
		t.Fatal(e)
	}
	terminalEvent := agentlink.MCPExecutionEvent{Claim: current[0].Claim, Binding: current[0].Admission.Binding, Sequence: current[0].Sequence, Phase: current[0].LastPhase}
	actualRecord, e := b.Tasks.Run(t.Context(), "main", current[0].Admission.Binding.Task.AgentID, current[0].Admission.Binding.RunID)
	if e != nil {
		t.Fatal(e)
	}
	terminalEvent.Result = actualRecord.Outcome
	duplicate, e := client.Event(t.Context(), terminalEvent)
	if e != nil || !duplicate.Duplicate {
		t.Fatal("event replay", duplicate, e)
	}
	stale := terminalEvent
	stale.Binding.RunID = "unrelated-run"
	if _, e = client.Event(t.Context(), stale); e == nil {
		t.Fatal("stale run report accepted")
	}
	forged := agentlink.MCPReceiverRegistration{Version: agentlink.MCPContractVersion, Receiver: agentlink.Identity{Address: agentlink.AssistantTarget("other-node", "main"), Name: "Other", Adapter: "pot"}, Capabilities: PotMCPReceiverCapabilities(), Epoch: b.options.BootID}
	if e = client.Register(t.Context(), forged); e == nil {
		t.Fatal("cross-node identity registered")
	}
	if e = b.savePotMCPDirectory(t.Context(), connection.ID, agentlink.Directory{NodeID: "replacement-node", Name: "Changed", Adapter: "pot", Targets: []agentlink.Endpoint{}}); !errors.Is(e, agentlink.ErrUnauthorized) {
		t.Fatal("remote node identity silently changed", e)
	}
	// A new SDK session and an identical submission preserve the existing effect.
	client.Close()
	client = potMCPClient(t, b, connection.ID)
	potMCPStep(t, client, receiver)
	fixtureCall(t, session, "pot_task_submit", input, &accepted)
	if !accepted.Receipt.Duplicate || driver.launches != 1 {
		t.Fatal("replayed effect", accepted, driver.launches)
	}
	// An old-run continuation creates a distinct real run on the same local Task.
	var continued MCPTaskAcceptance
	fixtureCall(t, session, "pot_task_input", agentlink.MCPTaskInput{RequestID: "continue", TaskID: completed.ID, RunID: completed.RunID, Text: "next"}, &continued)
	potMCPStep(t, client, receiver)
	potMCPStep(t, client, receiver)
	if e = b.Tasks.Tick(t.Context()); e != nil {
		t.Fatal(e)
	}
	potMCPStep(t, client, receiver)
	var next agentlink.Task
	fixtureCall(t, session, "pot_task_read", map[string]string{"task_id": completed.ID}, &next)
	if next.RunID == completed.RunID || next.State != "running" {
		t.Fatal("continuation", next)
	}
	fixtureCall(t, session, "pot_task_cancel", agentlink.MCPCancelInput{RequestID: "cancel", TaskID: next.ID, RunID: next.RunID}, &continued)
	potMCPStep(t, client, receiver)
	potMCPStep(t, client, receiver)
	if e = b.Tasks.Tick(t.Context()); e != nil {
		t.Fatal(e)
	}
	potMCPStep(t, client, receiver)
	fixtureCall(t, session, "pot_task_read", map[string]string{"task_id": next.ID}, &next)
	if next.State != "cancelled" {
		t.Fatal("cancel intent never terminal", next)
	}
	var legacy int
	if e = b.integrationDB.QueryRow("SELECT COUNT(*) FROM agent_link_history").Scan(&legacy); e != nil || legacy != 0 {
		t.Fatal("used legacy pairing", legacy, e)
	}
	if e = b.DisconnectPotMCP(t.Context(), connection.ID); e != nil {
		t.Fatal(e)
	}
	if !errors.Is(receiver.CheckActive(t.Context()), agentlink.ErrUnauthorized) {
		t.Fatal("disconnected receiver remained active")
	}
	// Restart the adapter with the same durable databases/claim journal. No driver
	// process or Control store is restarted by this fixture.
	receiver = &potMCPReceiver{s: b, identity: identity}
	if e = b.ReconnectPotMCP(t.Context(), connection.ID); e != nil {
		t.Fatal(e)
	}
	b.stopPotMCP(connection.ID)
	if e = b.RevokePotMCP(t.Context(), connection.ID); e != nil {
		t.Fatal(e)
	}
	if e = b.ReconnectPotMCP(t.Context(), connection.ID); !errors.Is(e, agentlink.ErrUnauthorized) {
		t.Fatal("revocation resurrected", e)
	}
}

func potMCPExposeClient(t *testing.T, s *Service, id string, client *mcpreceiver.Client) {
	t.Helper()
	done := make(chan struct{})
	close(done)
	s.potMCP.mu.Lock()
	s.potMCP.live = map[string]*potMCPLive{id: {client: client, online: true, cancel: func() {}, done: done}}
	s.potMCP.mu.Unlock()
	t.Cleanup(func() { s.potMCP.mu.Lock(); delete(s.potMCP.live, id); s.potMCP.mu.Unlock() })
}

func TestPotMCPBidirectionalAssistantTasksAndMail(t *testing.T) {
	a, driverA := potMCPControl(t, "pot-root-a")
	b, driverB := potMCPControl(t, "pot-root-b")
	server := potMCPServer(t, a)
	connection := potMCPAuthorize(t, b, server, "")
	client := potMCPClient(t, b, connection.ID)
	potMCPExposeClient(t, b, connection.ID, client)
	identity, _ := agentlink.NewMCPReceiver(connection.ID, b.options.RuntimeID)
	receiver := &potMCPReceiver{s: b, identity: identity}
	ctx := t.Context()
	targetA := agentlink.AssistantTarget(a.options.RuntimeID, "main")
	targetB := agentlink.AssistantTarget(b.options.RuntimeID, "main")
	// B's actual local root assistant calls A over its outbound client. No user
	// credential is used as a node actor and B publishes no MCP endpoint.
	info, e := b.CreateTask(ctx, "main", protocol.CreateTask{RequestID: "b-to-a", Text: "work on A", Target: &targetA})
	if e != nil {
		t.Fatal(e)
	}
	b.syncTaskExecutions(ctx)
	record, e := b.Tasks.Get(ctx, "main", info.ID)
	if e != nil || record.Execution.Receipt.ID == "" {
		t.Fatal(record, e)
	}
	if record.Execution.Request.From != targetB {
		t.Fatal("root assistant identity", record.Execution.Request)
	}
	runA := record.Execution.Receipt.RunID
	if e = a.Tasks.Tick(ctx); e != nil {
		t.Fatal(e)
	}
	driverA.complete(runA)
	if e = a.Tasks.Tick(ctx); e != nil {
		t.Fatal(e)
	}
	b.syncTaskExecutions(ctx)
	record, e = b.Tasks.Get(ctx, "main", info.ID)
	if e != nil || record.Info.Phase != protocol.TaskComplete || record.Info.RunID != runA {
		t.Fatal("outbound result lost actual run", record, e)
	}
	// A's root assistant submits through the same accepted receiver connection.
	reverse, e := a.CreateTask(ctx, "main", protocol.CreateTask{RequestID: "a-to-b", Text: "work on B", Target: &targetB})
	if e != nil {
		t.Fatal(e)
	}
	potMCPStep(t, client, receiver)
	potMCPStep(t, client, receiver)
	if e = b.Tasks.Tick(ctx); e != nil {
		t.Fatal(e)
	}
	potMCPStep(t, client, receiver)
	records, e := receiver.Records(ctx)
	if e != nil || len(records) != 1 || records[0].Admission == nil {
		t.Fatal(records, e)
	}
	if records[0].Work.Request.From != targetA {
		t.Fatal("reverse caller", records[0])
	}
	runB := records[0].Admission.Binding.RunID
	driverB.complete(runB)
	if e = b.Tasks.Tick(ctx); e != nil {
		t.Fatal(e)
	}
	potMCPStep(t, client, receiver)
	original, e := a.Tasks.Get(ctx, "main", reverse.ID)
	if e != nil || original.Info.Phase != protocol.TaskComplete || original.Execution.Receipt.RunID != runB {
		t.Fatal("reverse result", original, e)
	}
	// Both directions reuse the real Mailbox, including immutable reply causation.
	sent, e := b.sendAssistantMail(ctx, "b-mail", "main", protocol.SendMail{To: targetA, Text: "B reference"})
	if e != nil {
		t.Fatal(e)
	}
	c, e := b.PotMCPConnection(ctx, connection.ID)
	if e != nil {
		t.Fatal(e)
	}
	if e = b.syncPotMCPMail(ctx, c, client); e != nil {

		t.Fatal(e)
	}
	pending, e := a.Mailbox.Next(ctx, "main")
	if e != nil || pending == nil || pending.ID != sent.ID || pending.From != targetB {
		t.Fatal(pending, e)
	}
	if e = a.Mailbox.Claim(ctx, sent.ID); e != nil {
		t.Fatal(e)
	}
	parentCtx := context.WithValue(ctx, mailParentKey{}, *pending)
	reply, e := a.sendAssistantMail(parentCtx, "a-reply", "main", protocol.SendMail{To: targetB, Text: "A reply"})
	if e != nil {
		t.Fatal(e)
	}
	a.syncInboundPotMCPMail(ctx)
	potMCPStep(t, client, receiver)
	potMCPStep(t, client, receiver)
	received, e := b.Mailbox.Next(ctx, "main")
	if e != nil || received == nil || received.ID != reply.ID || received.ReplyTo == nil || *received.ReplyTo != sent.ID || received.ConversationID != sent.ConversationID || received.Hops != sent.Hops+1 {
		t.Fatal("causal envelope", received, e)
	}
	if e = b.syncPotMCPMail(ctx, c, client); e != nil {
		t.Fatal(e)
	}
	if driverA.launches != 1 || driverB.launches != 1 {
		t.Fatal("duplicate execution", driverA.launches, driverB.launches)
	}
	for _, s := range []*Service{a, b} {
		var legacy int
		if e = s.integrationDB.QueryRow("SELECT COUNT(*) FROM agent_link_history").Scan(&legacy); e != nil || legacy != 0 {
			t.Fatal("legacy connection used", legacy, e)
		}
	}
}

func TestPotMCPLocalExecutionIdentityAndWorkerRestrictions(t *testing.T) {
	a, _ := potMCPControl(t, "pot-proof-a")
	b, _ := potMCPControl(t, "pot-proof-b")
	server := potMCPServer(t, a)
	connection := potMCPAuthorize(t, b, server, "")
	client := potMCPClient(t, b, connection.ID)
	potMCPExposeClient(t, b, connection.ID, client)
	ctx := t.Context()
	record, e := b.Tasks.AcceptLocal(ctx, "main", protocol.CreateTask{RequestID: "local-task", Text: "safe local task"})
	if e != nil {
		t.Fatal(e)
	}
	actor := protocol.AgentRef{NodeID: b.options.RuntimeID, Kind: "task", AgentID: record.Info.ID}
	bound := context.WithValue(ctx, taskMessageRunKey{}, taskMessageRun{ID: record.RunID, Generation: record.Generation})
	execution, e := b.admitPotMCPExecution(bound, connection.ID, "main", actor)
	if e != nil {
		t.Fatal(e)
	}
	if execution.Claim.ClaimID != "" || execution.NodeExecutionID == "" || execution.Binding.Task != actor || execution.Binding.Actor != actor || execution.Binding.RunID != record.RunID {
		t.Fatal("invented execution", execution)
	}
	if _, e = b.admitPotMCPExecution(ctx, connection.ID, "main", actor); !errors.Is(e, agentlink.ErrUnauthorized) {
		t.Fatal("model supplied identity accepted", e)
	}
	stale := context.WithValue(ctx, taskMessageRunKey{}, taskMessageRun{ID: "stale-run", Generation: record.Generation})
	if _, e = b.admitPotMCPExecution(stale, connection.ID, "main", actor); !errors.Is(e, agentlink.ErrFenced) {
		t.Fatal("stale context", e)
	}
	for _, tool := range b.productTools(true) {
		if tool.Name == "create_task" {
			t.Fatal("Worker gained delegation")
		}
	}
	if _, e = b.sendAgentMessage(bound, "forbidden", "main", actor, protocol.SendMail{To: agentlink.AssistantTarget(a.options.RuntimeID, "main"), Text: "forbidden remote"}); e == nil {
		t.Fatal("Worker gained remote messaging")
	}
	var accepted MCPTaskAcceptance
	e = client.Call(ctx, "pot_receiver_task_submit", agentlink.MCPExecutionSubmit{Context: execution, Input: agentlink.MCPSubmitInput{RequestID: "forbidden-submit", Target: agentlink.AssistantTarget(a.options.RuntimeID, "main"), Text: "forbidden"}}, &accepted)
	if e == nil {
		t.Fatal("attestation granted Task delegation")
	}
	if e = b.Tasks.StopExpectedRun(ctx, "main", record.Info.ID, record.RunID); e != nil {
		t.Fatal(e)
	}
	if e = b.endPotMCPExecutions(ctx, connection.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = b.admitPotMCPExecution(bound, connection.ID, "main", actor); !errors.Is(e, agentlink.ErrFenced) {
		t.Fatal("ended run revived", e)
	}
	var active bool
	if e = a.Tasks.DB().QueryRow("SELECT active FROM mcp_node_executions WHERE connection_id=? AND execution_id=?", connection.ID, execution.NodeExecutionID).Scan(&active); e != nil || active {
		t.Fatal("remote binding not ended", active, e)
	}
}

func TestPotMCPReceiverOnlyOriginHumanCanApproveOnce(t *testing.T) {
	for _, action := range []string{"allow_once", "deny"} {
		t.Run(action, func(t *testing.T) {
			a, _ := potMCPControl(t, "pot-review-a")
			b, _ := potMCPControl(t, "pot-review-b")
			server := potMCPServer(t, a)
			connection := potMCPAuthorize(t, b, server, "")
			client := potMCPClient(t, b, connection.ID)
			potMCPExposeClient(t, b, connection.ID, client)
			identity, _ := agentlink.NewMCPReceiver(connection.ID, b.options.RuntimeID)
			receiver := &potMCPReceiver{s: b, identity: identity, remote: client}
			ctx := t.Context()
			target := agentlink.AssistantTarget(b.options.RuntimeID, "main")
			accepted, e := a.CreateTask(ctx, "main", protocol.CreateTask{RequestID: "review-task", Text: "safe fake approval", Target: &target})
			if e != nil {
				t.Fatal(e)
			}
			potMCPStep(t, client, receiver)
			potMCPStep(t, client, receiver)
			if e = b.Tasks.Tick(ctx); e != nil {
				t.Fatal(e)
			}
			potMCPStep(t, client, receiver)
			records, e := receiver.Records(ctx)
			if e != nil || len(records) != 1 {
				t.Fatal(records, e)
			}
			mapping := records[0]
			record, e := b.Tasks.Get(ctx, "main", mapping.Admission.Binding.Task.AgentID)
			if e != nil {
				t.Fatal(e)
			}
			if e = b.Permissions.Enroll("machine", "epoch"); e != nil {
				t.Fatal(e)
			}
			request := hostRequest("pot-review-call")
			request.Owner = "main"
			request.Task = record.Info.ID
			request.Turn = record.RunID
			request.Generation = record.Generation
			ready := make(chan struct{})
			result := make(chan error, 1)
			go func() {
				mode, e := b.Permissions.authorize(ctx, request, func() error { close(ready); return nil })
				if e == nil && mode != "host" {
					e = errors.New("unexpected authorization mode")
				}
				result <- e
			}()
			select {
			case <-ready:
			case <-time.After(2 * time.Second):
				t.Fatal("approval missing")
			}
			pending := b.Permissions.Pending("main")
			if len(pending) != 1 {
				t.Fatal(pending)
			}
			body, _ := json.Marshal(map[string]string{"id": pending[0].ID, "action": "allow_once"})
			if _, e = b.ManageApproval(ctx, "main", body); !errors.Is(e, agentlink.ErrUnauthorized) {
				t.Fatal("receiver human bypass", e)
			}
			if e = receiver.BeforeSnapshot(ctx, &mapping); e != nil {
				t.Fatal(e)
			}
			reviews := server.access.humanReviews(ctx, "main")
			if len(reviews) != 1 || reviews[0].Task != accepted.ID || reviews[0].Pending.Context.Binding.Actor.Kind != "task" {
				t.Fatal("original review ownership", reviews)
			}
			originID := mcpHumanApprovalID(reviews[0])
			body, _ = json.Marshal(map[string]string{"id": originID, "action": action})
			if _, e = a.ManageApproval(ctx, "main", body); e != nil {
				t.Fatal(e)
			}
			potMCPStep(t, client, receiver)
			controls, e := receiver.Records(ctx)
			if e != nil || len(controls) != 2 || controls[1].Control == nil {
				t.Fatal(controls, e)
			}
			for _, mutate := range []func(*agentlink.MCPControlDelivery){
				func(c *agentlink.MCPControlDelivery) { c.Approval.Epoch = "previous-process-epoch" },
				func(c *agentlink.MCPControlDelivery) { c.Binding.RunID = "previous-run" },
				func(c *agentlink.MCPControlDelivery) { c.Approval.RequestID = "foreign-request" },
				func(c *agentlink.MCPControlDelivery) { c.Caller.AgentID = "foreign-caller" },
			} {
				invalid := controls[1]
				control := *invalid.Control
				decision := *control.Approval
				control.Approval = &decision
				invalid.Control = &control
				mutate(&control)
				if e = receiver.Approval(ctx, &invalid); e == nil {
					t.Fatal("mismatched receiver decision accepted", control)
				}
				select {
				case e = <-result:
					t.Fatal("mismatched decision consumed pending request", e)
				default:
				}
			}
			potMCPStep(t, client, receiver)
			select {
			case e = <-result:
				if action == "allow_once" && e != nil || action == "deny" && !errors.Is(e, ErrHostDenied) {
					t.Fatal(e)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("human decision not applied")
			}
			if len(b.Permissions.grants) != 0 || len(b.Permissions.state.Rules) != 0 {
				t.Fatal("once decision created broader permissions")
			}
			records, e = receiver.Records(ctx)
			if e != nil || len(records) != 2 || !records[1].ControlApplied {
				t.Fatal(records, e)
			}
			if e = receiver.Approval(ctx, &records[1]); e != nil {
				t.Fatal("identical decision retry", e)
			}
			changed := records[1]
			copyControl := *changed.Control
			copyDecision := *copyControl.Approval
			copyDecision.Action = "deny"
			if action == "deny" {
				copyDecision.Action = "allow_once"
			}
			copyControl.Approval = &copyDecision
			changed.Control = &copyControl
			if e = receiver.Approval(ctx, &changed); e == nil {
				t.Fatal("conflicting human decision accepted")
			}
		})
	}
}

func TestPotMCPSameConnectionReverseHumanReview(t *testing.T) {
	for _, test := range []struct {
		name     string
		ordinary bool
		action   string
	}{{"root-assistant-once", false, "allow_once"}, {"direct-user-once", true, "allow_once"}, {"root-assistant-deny", false, "deny"}, {"direct-user-deny", true, "deny"}, {"root-assistant-terminal", false, "allow_once"}, {"direct-user-terminal", true, "allow_once"}} {
		t.Run(test.name, func(t *testing.T) {
			ordinary := test.ordinary
			a, driver := potMCPControl(t, "pot-human-a")
			b, _ := potMCPControl(t, "pot-human-b")
			server := potMCPServer(t, a)
			connection := potMCPAuthorize(t, b, server, "")
			client := potMCPClient(t, b, connection.ID)
			potMCPExposeClient(t, b, connection.ID, client)
			ctx := t.Context()
			target := agentlink.AssistantTarget(a.options.RuntimeID, "main")
			owner := "main"
			storedOwner := owner
			var info protocol.TaskInfo
			var e error
			request := protocol.CreateTask{RequestID: "reverse-human", Text: "safe fake A execution", Target: &target}
			if ordinary {
				owner = "agent-link:" + connection.ID + ":main"
				storedOwner = linkChatID(connection.ID, "main")
				raw, _ := json.Marshal(request)
				value, err := b.CallAgent(ctx, owner, agentlink.TaskSubmit, agentlink.Call{Body: raw})
				e = err
				if e == nil {
					info = value.(protocol.TaskInfo)
				}
			} else {
				info, e = b.CreateTask(ctx, owner, request)
			}
			if e != nil {
				t.Fatal(e)
			}
			b.syncTaskExecutions(ctx)
			record, e := b.Tasks.Get(ctx, storedOwner, info.ID)
			if e != nil || record == nil || record.Execution.Receipt.ID == "" {
				t.Fatal(record, e)
			}
			if e = a.Tasks.Tick(ctx); e != nil {
				t.Fatal(e)
			}
			b.syncTaskExecutions(ctx)
			var actualTask string
			if e = a.integrationDB.QueryRow("SELECT task_id FROM agent_link_received_tasks WHERE id=?", record.Execution.Receipt.ID).Scan(&actualTask); e != nil {
				t.Fatal(e)
			}
			actual, e := a.Tasks.Get(ctx, "main", actualTask)
			if e != nil {
				t.Fatal(e)
			}
			if e = a.Permissions.Enroll("machine", "epoch"); e != nil {
				t.Fatal(e)
			}
			host := hostRequest("reverse-human-call")
			host.Owner = "main"
			host.Task = actualTask
			host.Turn = actual.RunID
			host.Generation = actual.Generation
			permissionCtx, cancelPermission := context.WithCancel(ctx)
			defer cancelPermission()
			ready := make(chan struct{})
			finished := make(chan error, 1)
			go func() {
				_, e := a.Permissions.authorize(permissionCtx, host, func() error { close(ready); return nil })
				finished <- e
			}()
			select {
			case <-ready:
			case <-time.After(2 * time.Second):
				t.Fatal("pending missing")
			}
			pending := a.Permissions.Pending("main")
			if len(pending) != 1 {
				t.Fatal(pending)
			}
			body, _ := json.Marshal(map[string]string{"id": pending[0].ID, "action": "allow_once"})
			if _, e = a.ManageApproval(ctx, "main", body); !errors.Is(e, agentlink.ErrUnauthorized) {
				t.Fatal("receiving Pot human bypass", e)
			}
			view := b.MCPRemoteReviewView(ctx, owner)
			if len(view) != 1 {
				t.Fatal("original human did not see reverse request", view)
			}
			item := view[0].(map[string]any)
			if item["once_only"] != true {
				t.Fatal(item)
			}
			reviews := b.outboundPotMCPReviews(ctx, owner)
			if len(reviews) != 1 {
				t.Fatal(reviews)
			}
			review := reviews[0].review
			if review.ExecutionTask.AgentID != actualTask || review.RunID != actual.RunID || review.TaskID != record.Execution.Receipt.ID || review.Generation != actual.Generation {
				t.Fatal("review lost binding", review)
			}
			if ordinary && review.Caller.Kind != "user" || !ordinary && review.Caller != agentlink.AssistantTarget(b.options.RuntimeID, "main") {
				t.Fatal("caller changed role", review)
			}
			decision := agentlink.MCPHumanReviewDecision{Caller: review.Caller, RequestID: review.RequestID, TaskID: review.TaskID, ExecutionTask: review.ExecutionTask, RunID: review.RunID, Generation: review.Generation, Epoch: review.Epoch, ApprovalID: review.Approval.ID, User: b.reviewUser(connection.ID), Action: test.action}
			if ordinary {
				decision.User = review.Caller
			}
			for _, mutate := range []func(*agentlink.MCPHumanReviewDecision){
				func(d *agentlink.MCPHumanReviewDecision) { d.User.AgentID = "different-unassigned-human" },
				func(d *agentlink.MCPHumanReviewDecision) { d.User.NodeID = "wrong-node" },
				func(d *agentlink.MCPHumanReviewDecision) { d.User.Kind = "assistant" },
				func(d *agentlink.MCPHumanReviewDecision) { d.ExecutionTask.AgentID = "foreign-task" },
				func(d *agentlink.MCPHumanReviewDecision) { d.ApprovalID = "foreign-approval" },
				func(d *agentlink.MCPHumanReviewDecision) { d.Action = "allow_always" },
				func(d *agentlink.MCPHumanReviewDecision) { d.Epoch = "previous-process-epoch" },
				func(d *agentlink.MCPHumanReviewDecision) { d.RunID = "previous-run" },
				func(d *agentlink.MCPHumanReviewDecision) { d.Generation++ },
				func(d *agentlink.MCPHumanReviewDecision) { d.RequestID = "foreign-request" },
				func(d *agentlink.MCPHumanReviewDecision) { d.Caller.AgentID = "foreign-caller" },
			} {
				invalid := decision
				mutate(&invalid)
				var response struct {
					Accepted bool `json:"accepted"`
				}
				if e = client.Call(ctx, "pot_receiver_human_review_decide", invalid, &response); e == nil {
					t.Fatal("mismatched reverse decision accepted", invalid)
				}
				select {
				case e = <-finished:
					t.Fatal("mismatched decision consumed pending request", e)
				default:
				}
			}
			if strings.HasSuffix(test.name, "-terminal") {
				driver.complete(actual.RunID)
				if e = a.Tasks.Tick(ctx); e != nil {
					t.Fatal(e)
				}
				var response struct {
					Accepted bool `json:"accepted"`
				}
				if e = client.Call(ctx, "pot_receiver_human_review_decide", decision, &response); e == nil {
					t.Fatal("terminal task approved real pending")
				}
				select {
				case e = <-finished:
					t.Fatal("terminal decision consumed pending", e)
				default:
				}
				cancelPermission()
				<-finished
				return
			}
			body, _ = json.Marshal(map[string]string{"id": item["id"].(string), "action": test.action})
			if _, e = b.ManageApproval(ctx, owner, body); e != nil {
				t.Fatal(e)
			}
			select {
			case e = <-finished:
				if test.action == "allow_once" && e != nil || test.action == "deny" && !errors.Is(e, ErrHostDenied) {
					t.Fatal(e)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("reverse once decision missing")
			}
			var duplicate struct {
				Accepted bool `json:"accepted"`
			}
			// A retained identical decision may reconcile, or the consumed pending
			// may already be fenced. Neither can create another pending or effect.
			_ = client.Call(ctx, "pot_receiver_human_review_decide", decision, &duplicate)
			if len(a.Permissions.Pending("main")) != 0 {
				t.Fatal("duplicate decision created a pending request")
			}
			conflict := decision
			conflict.Action = "deny"
			if test.action == "deny" {
				conflict.Action = "allow_once"
			}
			if e = client.Call(ctx, "pot_receiver_human_review_decide", conflict, &duplicate); e == nil {
				t.Fatal("conflicting decision accepted")
			}
			if len(a.Permissions.grants) != 0 || len(a.Permissions.state.Rules) != 0 {
				t.Fatal("reverse once expanded permissions")
			}
			stale := agentlink.MCPHumanReviewDecision{Caller: review.Caller, RequestID: review.RequestID, TaskID: review.TaskID, ExecutionTask: review.ExecutionTask, RunID: "old-run", Generation: review.Generation, Epoch: review.Epoch, ApprovalID: review.Approval.ID, User: b.reviewUser(connection.ID), Action: "allow_once"}
			var response struct {
				Accepted bool `json:"accepted"`
			}
			if e = client.Call(ctx, "pot_receiver_human_review_decide", stale, &response); e == nil {
				t.Fatal("stale human decision accepted")
			}
		})
	}
}

func TestPotMCPControlReopenPreservesAcceptedHistoryAndIdentity(t *testing.T) {
	a, _ := potMCPControl(t, "pot-reopen-a")
	b, driver := potMCPControl(t, "pot-reopen-b")
	server := potMCPServer(t, a)
	connection := potMCPAuthorize(t, b, server, "")
	client := potMCPClient(t, b, connection.ID)
	identity, _ := agentlink.NewMCPReceiver(connection.ID, b.options.RuntimeID)
	receiver := &potMCPReceiver{s: b, identity: identity, remote: client}
	ctx := t.Context()
	target := agentlink.AssistantTarget(b.options.RuntimeID, "main")
	accepted, e := a.CreateTask(ctx, "main", protocol.CreateTask{RequestID: "persisted", Text: "safe persisted fake work", Target: &target})
	if e != nil {
		t.Fatal(e)
	}
	potMCPStep(t, client, receiver)
	potMCPStep(t, client, receiver)
	if e = b.Tasks.Tick(ctx); e != nil {
		t.Fatal(e)
	}
	records, e := receiver.Records(ctx)
	if e != nil || len(records) != 1 {
		t.Fatal(records, e)
	}
	binding := records[0].Admission.Binding
	driver.complete(binding.RunID)
	if e = b.Tasks.Tick(ctx); e != nil {
		t.Fatal(e)
	}
	potMCPStep(t, client, receiver)
	selected, e := b.Tasks.Prepare(ctx, "main", protocol.CreateTask{RequestID: "future", Text: "future"})
	if e != nil {
		t.Fatal(e)
	}
	if e = b.DisconnectPotMCP(ctx, connection.ID); e != nil {
		t.Fatal(e)
	}
	client.Close()
	options := b.options
	if e = b.Close(); e != nil {
		t.Fatal(e)
	}
	options.BootID = "reopened-boot"
	options.Manager, e = execution.New(options.RuntimeID, options.BootID, execution.DefaultLimits())
	if e != nil {
		t.Fatal(e)
	}
	paused := make(chan struct{})
	var initialized sync.Once
	options.Maintenance = func() bool { initialized.Do(func() { close(paused) }); return true }
	reopened, e := Open(options)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { reopened.Close() })
	select {
	case <-paused:
	case <-time.After(2 * time.Second):
		t.Fatal("reopened task loop did not pause")
	}
	reopened.Tasks, e = tasks.Open(reopened.Tasks.DB(), tasks.Options{NodeID: options.RuntimeID, Executor: driver, ResolveModel: func(context.Context, string, protocol.CreateTask) (tasks.Selection, error) { return selected, nil }})
	if e != nil {
		t.Fatal(e)
	}
	saved, e := reopened.PotMCPConnection(ctx, connection.ID)
	if e != nil || saved.State != "disconnected" || saved.NodeID != b.options.RuntimeID || saved.RemoteNodeID != a.options.RuntimeID || saved.Status == "connected" {
		t.Fatal("reopen invented online/identity", saved, e)
	}
	// Reactivate communication only; restart itself neither reassigns nor executes.
	if _, e = reopened.integrationDB.Exec("UPDATE mcp_receiver_connections SET state='active' WHERE id=?", connection.ID); e != nil {
		t.Fatal(e)
	}
	client = potMCPClient(t, reopened, connection.ID)
	receiver = &potMCPReceiver{s: reopened, identity: identity, remote: client}
	potMCPStep(t, client, receiver)
	history, e := reopened.Tasks.Get(ctx, "main", binding.Task.AgentID)
	if e != nil || history == nil || history.RunID != binding.RunID || history.Info.Phase != protocol.TaskComplete || driver.launches != 1 {
		t.Fatal("reopen changed accepted effect", history, driver.launches, e)
	}
	duplicate, e := a.CreateTask(ctx, "main", protocol.CreateTask{RequestID: "persisted", Text: "safe persisted fake work", Target: &target})
	if e != nil || duplicate.ID != accepted.ID {
		t.Fatal("origin retry lost receipt", duplicate, e)
	}
	var defaults int
	if e = reopened.integrationDB.QueryRow("SELECT COUNT(*) FROM agent_link_node_settings").Scan(&defaults); e != nil || defaults != 0 {
		t.Fatal("connect chose default assistant", defaults, e)
	}
}

func TestPotMCPDirectUserChatBothDirections(t *testing.T) {
	a, _ := potMCPControl(t, "pot-chat-a")
	b, _ := potMCPControl(t, "pot-chat-b")
	server := potMCPServer(t, a)
	connection := potMCPAuthorize(t, b, server, "")
	client := potMCPClient(t, b, connection.ID)
	potMCPExposeClient(t, b, connection.ID, client)
	identity, _ := agentlink.NewMCPReceiver(connection.ID, b.options.RuntimeID)
	receiver := &potMCPReceiver{s: b, identity: identity, remote: client}
	ctx := t.Context()
	for _, reverse := range []bool{false, true} {
		source, destination := b, a
		if reverse {
			source, destination = a, b
		}
		request := "ordinary-outbound"
		if reverse {
			request = "ordinary-reverse"
		}
		receipt, e := source.SendLinkChat(ctx, connection.ID, "main", request, "direct user question")
		if e != nil {
			t.Fatal(e)
		}
		if reverse {
			a.syncInboundPotMCPMail(ctx)
			potMCPStep(t, client, receiver)
			potMCPStep(t, client, receiver)
		}
		incoming, e := destination.Mailbox.Next(ctx, "main")
		if e != nil || incoming == nil || incoming.ID != receipt.MessageID || incoming.From.Kind != "user" {
			t.Fatal(incoming, e)
		}
		if e = destination.Mailbox.Claim(ctx, incoming.ID); e != nil {
			t.Fatal(e)
		}
		bound := context.WithValue(ctx, mailParentKey{}, *incoming)
		reply, e := destination.sendAssistantMail(bound, request+"-reply", "main", protocol.SendMail{To: incoming.From, Text: "direct reply"})
		if e != nil {
			t.Fatal(e)
		}
		if reverse {
			c, e := b.PotMCPConnection(ctx, connection.ID)
			if e != nil {
				t.Fatal(e)
			}
			if e = b.syncPotMCPMail(ctx, c, client); e != nil {
				t.Fatal(e)
			}
		}
		c, e := source.PotMCPConnection(ctx, connection.ID)
		if e != nil {
			t.Fatal(e)
		}
		source.syncPotMCPChats(ctx, c)
		var displayed string
		if e = source.integrationDB.QueryRow("SELECT text FROM agent_link_chat WHERE id=? AND role='assistant'", reply.ID).Scan(&displayed); e != nil || displayed != "direct reply" {
			t.Fatal("direct UI reply", displayed, e)
		}
		repeated, e := source.SendLinkChat(ctx, connection.ID, "main", request, "direct user question")
		if e != nil || !repeated.Duplicate || repeated.MessageID != receipt.MessageID {
			t.Fatal("direct send retry", repeated, e)
		}
	}
}

func TestPotMCPRenewalAndLiveConnectionState(t *testing.T) {
	a, _ := potMCPControl(t, "pot-live-a")
	b, _ := potMCPControl(t, "pot-live-b")
	server := potMCPServer(t, a)
	connection := potMCPAuthorize(t, b, server, "")
	b.startPotMCP(connection.ID)
	deadline := time.Now().Add(4 * time.Second)
	for {
		current, e := b.PotMCPConnection(t.Context(), connection.ID)
		if e != nil {
			t.Fatal(e)
		}
		if current.Status == "connected" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("receiver never became live", current)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if e := b.DisconnectPotMCP(t.Context(), connection.ID); e != nil {
		t.Fatal(e)
	}
	current, e := b.PotMCPConnection(t.Context(), connection.ID)
	if e != nil || current.Status != "disconnected" || current.LastSeenAt == 0 {
		t.Fatal("disconnect lost actual state", current, e)
	}
	if _, e = b.integrationDB.Exec("UPDATE mcp_receiver_connections SET claim_request_id='original-poll' WHERE id=?", connection.ID); e != nil {
		t.Fatal(e)
	}
	renewed := potMCPAuthorize(t, b, server, connection.ID)
	if renewed.ID != connection.ID {
		t.Fatal("renewal changed logical identity", renewed)
	}
	var request string
	if e = b.integrationDB.QueryRow("SELECT claim_request_id FROM mcp_receiver_connections WHERE id=?", connection.ID).Scan(&request); e != nil || request != "original-poll" {
		t.Fatal("renewal lost claim reconciliation", request, e)
	}
	if e = b.ReconnectPotMCP(t.Context(), connection.ID); e != nil {
		t.Fatal(e)
	}
	deadline = time.Now().Add(4 * time.Second)
	for {
		current, e = b.PotMCPConnection(t.Context(), connection.ID)
		if e != nil {
			t.Fatal(e)
		}
		if current.Status == "connected" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("renewed receiver not live", current)
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, service := range []*Service{a, b} {
		if _, e = service.AgentLinks(t.Context()); e != nil {
			t.Fatal(e)
		}
		var defaults int
		if e = service.integrationDB.QueryRow("SELECT COUNT(*) FROM agent_link_node_settings").Scan(&defaults); e != nil || defaults != 0 {
			t.Fatal("connection page selected default", defaults, e)
		}
	}
	incoming, e := a.PotMCPConnectionView(t.Context())
	if e != nil || len(incoming) != 1 || !incoming[0].Inbound || incoming[0].Status != "connected" {
		t.Fatal("incoming view invented availability", incoming, e)
	}
	if e = a.RevokePotMCP(t.Context(), connection.ID); e != nil {
		t.Fatal(e)
	}
	if e = b.DisconnectPotMCP(t.Context(), connection.ID); e != nil {
		t.Fatal(e)
	}
}
