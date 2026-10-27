package agenthost

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/control"
	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/execution"
	"github.com/jjyr/pot/pkg/mcp"
	"github.com/jjyr/pot/pkg/protocol"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func receiverGatewayPot(t *testing.T) (string, *control.Service, protocol.AgentRef) {
	t.Helper()
	manager, e := execution.New("gateway-pot", "gateway-boot", execution.Limits{MaxActive: 1, MaxQueued: 8})
	if e != nil {
		t.Fatal(e)
	}
	keys, e := secrets.NewMemoryKey(make([]byte, 32))
	if e != nil {
		t.Fatal(e)
	}
	s, e := control.Open(control.Options{Root: t.TempDir(), RuntimeID: "gateway-pot", BootID: "gateway-boot", Manager: manager, Keys: keys, Maintenance: func() bool { return true }})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	owner, e := s.Registry.Create(context.Background(), "gateway-owner", "Gateway fixture")
	if e != nil {
		t.Fatal(e)
	}
	profile, e := s.Profiles.Save(context.Background(), control.SaveProfile{RequestID: "profile", ProfileID: "fixture", Name: "Fixture", Model: "fixture", Endpoint: "http://127.0.0.1:1", Protocol: "openai_chat", APIKey: "synthetic", MaxOutputTokens: 100})
	if e != nil {
		t.Fatal(e)
	}
	spec, e := s.Profiles.Resolve(profile.ID)
	if e != nil {
		t.Fatal(e)
	}
	conversation, e := s.Conversation(context.Background(), owner.ID)
	if e != nil {
		t.Fatal(e)
	}
	if e = conversation.Configure(context.Background(), spec, 0); e != nil {
		t.Fatal(e)
	}
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	access, e := s.NewMCPAccess(control.MCPOAuthOptions{Origin: server.URL, OwnerSession: func(r *http.Request) (string, bool) {
		return "synthetic-owner", r.Header.Get("X-Fixture-Owner") == "synthetic-owner"
	}}, func(_ context.Context, _ agentlink.MCPReceiver, target protocol.AgentRef) (agentlink.Description, error) {
		if target.Kind == "node" {
			return Codex().ReceiverCapabilities(), nil
		}
		return agentlink.Description{Version: 1, Scope: "peer", Operations: []agentlink.Operation{agentlink.ChatSend, agentlink.ChatRead, agentlink.ChatReceipt}}, nil
	})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(access.Close)
	mux.Handle("/", access)
	return server.URL, s, agentlink.AssistantTarget("gateway-pot", owner.ID)
}

func TestReceiverRealHTTPNativeAssistantReverseAndHumanApproval(t *testing.T) {
	origin, s, target := receiverGatewayPot(t)
	h, _ := testHost(t)
	defer h.close()
	h.backend.binary = fakeBinary
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if e := h.backend.ownerConfigure(ctx, assistant{ID: "codex", Name: "Native fixture", Workspace: h.backend.root + "/workspace", Mode: "read-only", Model: "fixture"}); e != nil {
		t.Fatal(e)
	}
	token, connection, e := authorizeReceiver(ctx, origin+"/mcp", h.backend.nodeID, receiverConsentBrowser(t, origin))
	if e != nil {
		t.Fatal(e)
	}
	if e = h.saveReceiverGrant(ctx, receiverGrant{ConnectionID: connection, URL: origin + "/mcp", NodeID: h.backend.nodeID, Token: token}); e != nil {
		t.Fatal(e)
	}
	identity, _ := agentlink.NewMCPReceiver(connection, h.backend.nodeID)
	client, e := connectReceiver(ctx, origin+"/mcp", token.AccessToken, identity)
	if e != nil {
		t.Fatal(e)
	}
	defer client.close()
	node := agentlink.MCPReceiverRegistration{Version: 1, Receiver: agentlink.Identity{Address: agentlink.NodeTarget(h.backend.nodeID), Name: "Fake native", Adapter: "codex"}, Capabilities: Codex().ReceiverCapabilities(), Epoch: h.receiverEpoch}
	if e = client.Register(ctx, node); e != nil {
		t.Fatal(e)
	}
	if e = h.registerReceiverAssistants(ctx, client); e != nil {
		t.Fatal(e)
	}
	callerToken, callerID, e := authorizeReceiver(ctx, origin+"/mcp", "unused-user-declaration", receiverConsentBrowser(t, origin))
	if e != nil {
		t.Fatal(e)
	}
	callerIdentity, _ := agentlink.NewMCPReceiver(callerID, "unused-user-declaration")
	caller, e := connectReceiver(ctx, origin+"/mcp", callerToken.AccessToken, callerIdentity)
	if e != nil {
		t.Fatal(e)
	}
	defer caller.close()
	var mail control.MCPAccessMessageAcceptance
	if e = caller.call(ctx, "pot_message_send", agentlink.MCPMessageInput{RequestID: "native-assistant", Message: protocol.SendMail{To: agentlink.AssistantTarget(h.backend.nodeID, "codex"), Text: "hang"}}, &mail); e != nil {
		t.Fatal(e)
	}
	pump := &receiverPump{backend: h.backend, control: h.db, identity: identity, remote: client, epoch: h.receiverEpoch}
	receiverMustStep(t, pump)
	receiverMustStep(t, pump)
	var delivery string
	if e = h.tasks.QueryRow("SELECT delivery_id FROM mcp_receiver_journal WHERE connection_id=?", connection).Scan(&delivery); e != nil {
		t.Fatal(e)
	}
	record, e := readReceiverRecord(ctx, h.tasks, connection, delivery)
	if e != nil {
		t.Fatal(e)
	}
	j, e := h.backend.read(ctx, record.Work.Request.From, record.Admission.Binding.Task.AgentID)
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan struct{})
	go func() { defer close(done); h.backend.execute(ctx, j) }()
	awaitTask(t, h.backend, j.Request.From, j.Task.ID, "running")
	local := agentHostClient(h.token)
	defer local.client.CloseIdleConnections()
	local.client.Transport = hostRoundTripper(func(r *http.Request) (*http.Response, error) {
		r.RemoteAddr = "127.0.0.1:23456"
		r.Host = hostListen
		w := httptest.NewRecorder()
		h.handler().ServeHTTP(w, r)
		return w.Result(), nil
	})
	a, b := sdk.NewInMemoryTransports()
	go executionMCPServer(local, j.Task.ID, j.Task.RunID, "codex").Run(ctx, a)
	model, e := mcp.Connect(ctx, b, mcp.Options{})
	if e != nil {
		t.Fatal(e)
	}
	defer model.Close()
	// This is an SDK model-tool client while the actual fake native process is
	// active. It is not a live Codex MCP subprocess/plugin validation.
	result, e := model.CallTool(ctx, "pot_task_submit", json.RawMessage(encoded(agentlink.MCPSubmitInput{RequestID: "model-reverse-task", Target: target, Text: "real local Task admission; maintenance prevents model execution"})))
	if e != nil || result.IsError {
		t.Fatal("native reverse task failed", result, e)
	}
	result, e = model.CallTool(ctx, "pot_message", json.RawMessage(encoded(agentlink.MCPMessageInput{RequestID: "model-reverse-message", Message: protocol.SendMail{To: target, Text: "causal reference"}})))
	if e != nil || result.IsError {
		t.Fatal("native reverse mail failed", result, e)
	}
	var accepted control.MCPAccessMessageAcceptance
	raw, _ := json.Marshal(result.StructuredContent)
	if json.Unmarshal(raw, &accepted) != nil || accepted.Message.Message.From != record.Work.Message.To || accepted.Message.Message.Hops != record.Work.Message.Hops+1 {
		t.Fatal("native causal actor/hops lost", string(raw))
	}
	// Sender/execution spoofing fails at the typed local model boundary.
	result, e = model.CallTool(ctx, "pot_message", json.RawMessage(`{"request_id":"spoof","message":{"to":{"node_id":"gateway-pot","kind":"assistant","agent_id":"x"},"text":"x"},"from":{"kind":"user"}}`))
	if e == nil && !result.IsError {
		t.Fatal("model supplied sender")
	}
	h.backend.mu.Lock()
	stop := h.backend.active[j.Task.ID]
	h.backend.mu.Unlock()
	stop()
	<-done
	receiverMustStep(t, pump)
	// An ordinary OAuth user's direct native Task remains a Task actor. Its
	// human request reaches the original human control plane without forging an assistant.
	var task control.MCPTaskAcceptance
	if e = caller.call(ctx, "pot_task_submit", agentlink.MCPSubmitInput{RequestID: "native-human", Target: node.Receiver.Address, Text: "approve"}, &task); e != nil {
		t.Fatal(e)
	}
	receiverMustStep(t, pump)
	receiverMustStep(t, pump)
	var rawRecord string
	if e = h.tasks.QueryRow("SELECT body FROM mcp_receiver_journal WHERE json_extract(body,'$.work.request.request_id')='native-human'").Scan(&rawRecord); e != nil {
		t.Fatal(e)
	}
	var reviewRecord receiverRecord
	if json.Unmarshal([]byte(rawRecord), &reviewRecord) != nil {
		t.Fatal(rawRecord)
	}
	reviewJob, e := h.backend.read(ctx, reviewRecord.Work.Request.From, reviewRecord.Admission.Binding.Task.AgentID)
	if e != nil {
		t.Fatal(e)
	}
	reviewDone := make(chan struct{})
	go func() { defer close(reviewDone); h.backend.runTask(ctx, reviewJob) }()
	awaitTask(t, h.backend, reviewJob.Request.From, reviewJob.Task.ID, "waiting_approval")
	receiverMustStep(t, pump)
	view := s.ApprovalView(ctx, target.AgentID)
	pending := view["pending"].([]any)
	if len(pending) != 1 {
		t.Fatal("remote human approval missing", view)
	}
	id := pending[0].(map[string]any)["id"].(string)
	if _, e = s.ManageApproval(ctx, target.AgentID, []byte(encoded(map[string]any{"id": id, "action": "allow_once"}))); e != nil {
		t.Fatal(e)
	}
	receiverMustStep(t, pump)
	receiverMustStep(t, pump)
	<-reviewDone
	receiverMustStep(t, pump)
	if current, e := h.backend.read(ctx, reviewJob.Request.From, reviewJob.Task.ID); e != nil || current.Task.State != "completed" || current.Task.RunID != reviewJob.Task.RunID {
		t.Fatal("native once decision did not complete", current, e)
	}
	if _, e = s.ManageApproval(ctx, target.AgentID, []byte(encoded(map[string]any{"id": id, "action": "allow_once"}))); e == nil {
		t.Fatal("native decision repeated")
	}
}
