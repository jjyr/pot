package agenthost

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/control"
	"github.com/jjyr/pot/internal/tasks"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

// This browser is a synthetic owner consent fixture, never a real login. The
// service, OAuth+PKCE client, SDK HTTP transport, SQLite admissions/projections,
// native Host supervision and cleanup are actual Pot code. Only the native
// executable is fake; these tests do not claim live Codex/AGY validation.
func receiverConsentBrowser(t *testing.T, origin string) func(string) error {
	t.Helper()
	return func(auth string) error {
		client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		request, err := http.NewRequest("GET", auth, nil)
		if err != nil {
			return err
		}
		request.Header.Set("X-Fixture-Owner", "synthetic-owner")
		response, err := client.Do(request)
		if err != nil {
			return err
		}
		response.Body.Close()
		if response.StatusCode != http.StatusSeeOther {
			return errors.New("fixture_authorize_rejected")
		}
		location, err := url.Parse(response.Header.Get("Location"))
		if err != nil {
			return err
		}
		raw, _ := json.Marshal(map[string]any{"request_id": location.Query().Get("request"), "approve": true})
		request, err = http.NewRequest("POST", origin+"/oauth/consent", strings.NewReader(string(raw)))
		if err != nil {
			return err
		}
		request.Header.Set("X-Fixture-Owner", "synthetic-owner")
		request.Header.Set("Origin", origin)
		request.Header.Set("Content-Type", "application/json")
		response, err = client.Do(request)
		if err != nil {
			return err
		}
		defer response.Body.Close()
		var decision struct {
			Redirect string `json:"redirect"`
		}
		if response.StatusCode != 200 || json.NewDecoder(response.Body).Decode(&decision) != nil {
			return errors.New("fixture_consent_rejected")
		}
		response, err = http.Get(decision.Redirect)
		if err == nil {
			response.Body.Close()
		}
		return err
	}
}

func receiverPotHTTP(t *testing.T, runtimes ...Runtime) (string, *control.MCPAccessQueue) {
	t.Helper()
	root := t.TempDir()
	db, err := storage.OpenDatabase(filepath.Join(root, "control.sqlite"), storage.ControlDatabase)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	taskDB, err := storage.OpenDatabase(filepath.Join(root, "tasks.sqlite"), storage.TasksDatabase)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { taskDB.Close() })
	catalogue, err := tasks.Open(taskDB, tasks.Options{NodeID: "pot-service"})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	oauth, err := control.NewMCPOAuthServer(control.MCPOAuthOptions{DB: db, Origin: server.URL, NodeID: "pot-service", OwnerSession: func(r *http.Request) (string, bool) {
		return "synthetic-owner", r.Header.Get("X-Fixture-Owner") == "synthetic-owner"
	}})
	if err != nil {
		t.Fatal(err)
	}
	queue := &control.MCPAccessQueue{DB: taskDB, Tasks: catalogue, NodeID: "pot-service"}
	access, err := control.NewMCPAccess(control.MCPAccessOptions{OAuth: oauth, Queue: queue, ReceiverCapabilities: func(_ context.Context, receiver agentlink.MCPReceiver, target protocol.AgentRef) (agentlink.Description, error) {
		if target != agentlink.NodeTarget(receiver.NodeID()) {
			return agentlink.Description{}, agentlink.ErrUnauthorized
		}
		return runtimeOrDefault(runtimes).ReceiverCapabilities(), nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(access.Close)
	mux.Handle("/", access)
	return server.URL, queue
}

type receiverLostEvent struct {
	receiverRemote
	lost bool
}

func (c *receiverLostEvent) Event(ctx context.Context, e agentlink.MCPExecutionEvent) (agentlink.MCPEventReceipt, error) {
	receipt, err := c.receiverRemote.Event(ctx, e)
	if err == nil && !c.lost && receiverTerminal(e.Phase) {
		c.lost = true
		return agentlink.MCPEventReceipt{}, errors.New("fixture_terminal_response_lost")
	}
	return receipt, err
}

func TestReceiverRealPotHTTPAdmissionRecoveryAndContinuation(t *testing.T) {
	for _, retiredCount := range []int{0, 32} {
		t.Run(strconv.Itoa(retiredCount), func(t *testing.T) { receiverHTTPContinuationFixture(t, retiredCount) })
	}
}

func receiverHTTPContinuationFixture(t *testing.T, retiredCount int, runtimes ...Runtime) {
	t.Helper()
	adapter := runtimeOrDefault(runtimes)
	p, _, _ := receiverFixture(t)
	var agyHost *agentHost
	var agyWorkspace string
	if adapter.Kind == "antigravity" {
		adapter.Binary = fakeAgyBinary
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		adapter.Listen = listener.Addr().String()
		root := filepath.Join(t.TempDir(), "custom-agy-host")
		h, err := openAgentHost(root, adapter)
		if err != nil {
			listener.Close()
			t.Fatal(err)
		}
		defer h.close()
		agyHost = h
		server := &http.Server{Handler: h.handler()}
		go server.Serve(listener)
		defer server.Close()
		p.backend = h.backend
		agyWorkspace = t.TempDir()
		binary, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(agyWorkspace, "execution-tools-binary"), []byte(binary), 0600); err != nil {
			t.Fatal(err)
		}
		if err = h.backend.ownerExecutor(context.Background(), agyWorkspace, "read-only", "model-one", ""); err != nil {
			t.Fatal(err)
		}
		t.Setenv("POT_AGENT_UNTRUSTED", "must-not-inherit")
	}
	origin, _ := receiverPotHTTP(t, adapter)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	token, connection, err := authorizeReceiver(ctx, origin+"/mcp", p.backend.nodeID, receiverConsentBrowser(t, origin))
	if err != nil {
		t.Fatal(err)
	}
	identity, err := agentlink.NewMCPReceiver(connection, p.backend.nodeID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.control.Exec("UPDATE mcp_receiver_connections SET id=?,url=?,node_id=? WHERE id=?", connection, origin+"/mcp", p.backend.nodeID, p.identity.ConnectionID()); err != nil {
		t.Fatal(err)
	}
	p.identity = identity
	client, err := connectReceiver(ctx, origin+"/mcp", token.AccessToken, identity)
	if err != nil {
		t.Fatal(err)
	}
	defer client.close()
	registration := agentlink.MCPReceiverRegistration{Version: 1, Receiver: agentlink.Identity{Address: agentlink.NodeTarget(p.backend.nodeID), Name: "Fixture native receiver", Adapter: adapter.Kind}, Capabilities: adapter.ReceiverCapabilities()}
	if err = client.Register(ctx, registration); err != nil {
		t.Fatal(err)
	}
	// This second public OAuth connection is an independent user-kind caller.
	// Its optional receiver declaration is not registered or used for execution.
	callerToken, callerConnection, err := authorizeReceiver(ctx, origin+"/mcp", "unused-caller-node", receiverConsentBrowser(t, origin))
	if err != nil {
		t.Fatal(err)
	}
	callerIdentity, _ := agentlink.NewMCPReceiver(callerConnection, "unused-caller-node")
	caller, err := connectReceiver(ctx, origin+"/mcp", callerToken.AccessToken, callerIdentity)
	if err != nil {
		t.Fatal(err)
	}
	defer caller.close()
	var accepted control.MCPTaskAcceptance
	submit := agentlink.MCPSubmitInput{RequestID: "http-submit", Target: registration.Receiver.Address, Text: "actual Pot HTTP fixture"}
	if err = caller.call(ctx, "pot_task_submit", submit, &accepted); err != nil {
		t.Fatal(err)
	}
	p.remote = client
	receiverMustStep(t, p)
	receiverMustStep(t, p)
	var delivery string
	if err = p.backend.db.QueryRow("SELECT delivery_id FROM mcp_receiver_journal WHERE connection_id=?", connection).Scan(&delivery); err != nil {
		t.Fatal(err)
	}
	r, err := readReceiverRecord(ctx, p.backend.db, connection, delivery)
	if err != nil {
		t.Fatal(err)
	}
	if r.Work.Request.From.Kind != "user" || r.Claim.Binding.Task.AgentID != accepted.Task.ID || r.Admission.Binding.Task.AgentID == accepted.Task.ID {
		t.Fatal("origin caller/task mapping lost", r)
	}
	j, err := p.backend.read(ctx, r.Work.Request.From, r.Admission.Binding.Task.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	p.backend.runTask(ctx, j)
	firstThread := ""
	if agyHost != nil {
		assertAgyExecutionProbe(t, agyHost, agyWorkspace, j.Task.ID, j.Task.RunID)
		if err = p.backend.db.QueryRow("SELECT thread_id FROM agent_adapter_sessions WHERE task_id=?", j.Task.ID).Scan(&firstThread); err != nil || firstThread == "" {
			t.Fatal("missing AGY conversation", err)
		}
	}
	loss := &receiverLostEvent{receiverRemote: client}
	p.remote = loss
	if err = p.step(ctx); err == nil || !loss.lost {
		t.Fatal("terminal response loss not injected", err)
	}
	pending, err := readReceiverRecord(ctx, p.backend.db, connection, delivery)
	if err != nil || pending.Outbox == nil {
		t.Fatal("event was not durable", err)
	}
	// A fresh SDK session reuses the same logical receiver and only resends the
	// stored event. The original native job remains completed and is not launched.
	reconnected, err := connectReceiver(ctx, origin+"/mcp", token.AccessToken, identity)
	if err != nil {
		t.Fatal(err)
	}
	defer reconnected.close()
	if err = reconnected.Register(ctx, registration); err != nil {
		t.Fatal(err)
	}
	p.remote = reconnected
	receiverMustStep(t, p)
	var task agentlink.Task
	read := struct {
		TaskID string `json:"task_id"`
	}{accepted.Task.ID}
	if err = caller.call(ctx, "pot_task_read", read, &task); err != nil || task.State != string(protocol.TaskComplete) || task.RunID != r.Admission.Binding.RunID {
		t.Fatal("missing actual Pot terminal projection", task, err)
	}
	assertRetiredUnchanged := receiverRetiredJobsFixture(t, p, retiredCount)
	oldRun := task.RunID
	var inputAccepted control.MCPTaskAcceptance
	input := agentlink.MCPTaskInput{RequestID: "http-continuation", TaskID: task.ID, RunID: oldRun, Text: "HTTP continuation"}
	if err = caller.call(ctx, "pot_task_input", input, &inputAccepted); err != nil {
		t.Fatal(err)
	}
	receiverMustStep(t, p)
	receiverMustStep(t, p)
	current, err := p.backend.read(ctx, r.Work.Request.From, j.Task.ID)
	if err != nil || current.Task.RunID == oldRun || current.Task.State != "queued" {
		t.Fatal("no real new run", current, err)
	}
	p.backend.runTask(ctx, current)
	if agyHost != nil {
		assertAgyExecutionProbe(t, agyHost, agyWorkspace, current.Task.ID, current.Task.RunID)
		var nextThread string
		if err = p.backend.db.QueryRow("SELECT thread_id FROM agent_adapter_sessions WHERE task_id=?", current.Task.ID).Scan(&nextThread); err != nil || nextThread != firstThread {
			t.Fatal("AGY continuation changed conversation", err)
		}
	}
	receiverMustStep(t, p)
	if err = caller.call(ctx, "pot_task_read", read, &task); err != nil || task.State != string(protocol.TaskComplete) || task.RunID != current.Task.RunID {
		t.Fatal(task, err)
	}
	var jobs, events int
	p.backend.db.QueryRow("SELECT count(*) FROM agent_adapter_jobs").Scan(&jobs)
	p.backend.db.QueryRow("SELECT count(*) FROM mcp_receiver_journal WHERE json_extract(body,'$.last_phase')='complete'").Scan(&events)
	if jobs != 1+retiredCount || events != 2 {
		t.Fatal("duplicate work or missing continued terminal receipts", jobs, events)
	}

	assertRetiredUnchanged()
}

func TestReceiverRealPotOAuthRenewalPreservesConnectionAndPendingClaim(t *testing.T) {
	h, _ := testHost(t)
	defer h.close()
	origin, _ := receiverPotHTTP(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	token, id, err := authorizeReceiver(ctx, origin+"/mcp", h.backend.nodeID, receiverConsentBrowser(t, origin))
	if err != nil {
		t.Fatal(err)
	}
	grant := receiverGrant{id, origin + "/mcp", h.backend.nodeID, token}
	if err = h.saveReceiverGrant(ctx, grant); err != nil {
		t.Fatal(err)
	}
	if _, err = h.db.Exec("UPDATE mcp_receiver_connections SET state='disconnected',claim_request_id='pending-claim' WHERE id=?", id); err != nil {
		t.Fatal(err)
	}
	metadata, err := h.receiverAuthorization(ctx, id)
	if err != nil || metadata.ClientID != token.ClientID || metadata.NodeID != h.backend.nodeID {
		t.Fatal("missing private OAuth client metadata", err)
	}
	renewed, same, err := authorizeReceiver(ctx, origin+"/mcp", metadata.NodeID, receiverConsentBrowser(t, origin), metadata.ClientID)
	if err != nil || same != id || renewed.ClientID != token.ClientID || renewed.AccessToken == token.AccessToken {
		t.Fatal("renewal replaced logical identity", err)
	}
	grant.Token = renewed
	if err = h.saveReceiverGrant(ctx, grant); err != nil {
		t.Fatal(err)
	}
	var pending, state string
	if err = h.db.QueryRow("SELECT claim_request_id,state FROM mcp_receiver_connections WHERE id=?", id).Scan(&pending, &state); err != nil || pending != "pending-claim" || state != "active" {
		t.Fatal("renewal erased recovery boundary", pending, state, err)
	}
}

func TestAntigravityReceiverHTTPExecutionContextAndContinuation(t *testing.T) {
	receiverHTTPContinuationFixture(t, 0, Antigravity())
}

func assertAgyExecutionProbe(t *testing.T, h *agentHost, workspace, jobID, runID string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(workspace, "context-"+runID+".json"))
	var binding agentlink.MCPExecutionBinding
	if err != nil || json.Unmarshal(raw, &binding) != nil || binding.Task != (protocol.AgentRef{NodeID: h.backend.nodeID, Kind: "task", AgentID: jobID}) || binding.Actor != binding.Task || binding.RunID != runID {
		t.Fatalf("missing exact AGY execution context: %s %v", raw, err)
	}
	raw, err = os.ReadFile(filepath.Join(workspace, "preflight-"+runID+".json"))
	var preflight map[string]string
	realWorkspace, _ := filepath.EvalSymlinks(workspace)
	if err != nil || json.Unmarshal(raw, &preflight) != nil || preflight["workspace"] != realWorkspace || preflight["root"] != h.backend.root || preflight["listen"] != h.runtime().Listen || preflight["job"] != jobID || preflight["run"] != runID || preflight["inherited"] != "" {
		t.Fatalf("AGY preflight differs from execution scope: %s %v", raw, err)
	}
}
