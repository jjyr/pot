package control

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/tasks"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/mcp"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

type mcpAccessFixture struct {
	access *MCPAccess
	oauth  *MCPOAuthServer
	queue  *MCPAccessQueue
	http   *httptest.Server
}

func mcpAccessTestFixture(t *testing.T) *mcpAccessFixture {
	t.Helper()
	root := t.TempDir()
	controlDB, e := storage.OpenDatabase(filepath.Join(root, "control.sqlite"), storage.ControlDatabase)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { controlDB.Close() })
	taskDB, e := storage.OpenDatabase(filepath.Join(root, "tasks.sqlite"), storage.TasksDatabase)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { taskDB.Close() })
	catalogue, e := tasks.Open(taskDB, tasks.Options{NodeID: "pot-fixture"})
	if e != nil {
		t.Fatal(e)
	}
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	oauth, e := NewMCPOAuthServer(MCPOAuthOptions{DB: controlDB, Origin: server.URL, NodeID: "pot-fixture", LoginURL: "/mcp-connect.html", OwnerSession: func(r *http.Request) (string, bool) {
		return r.Header.Get("X-Fixture-Owner"), r.Header.Get("X-Fixture-Owner") != ""
	}})
	if e != nil {
		t.Fatal(e)
	}
	queue := &MCPAccessQueue{DB: taskDB, Tasks: catalogue, NodeID: "pot-fixture"}
	access, e := NewMCPAccess(MCPAccessOptions{OAuth: oauth, Queue: queue, ReceiverCapabilities: func(_ context.Context, r agentlink.MCPReceiver, target protocol.AgentRef) (agentlink.Description, error) {
		if target != agentlink.NodeTarget(r.NodeID()) {
			return agentlink.Description{}, agentlink.ErrUnsupported
		}
		return fixtureReceiverDescription(), nil
	}})
	if e != nil {
		t.Fatal(e)
	}
	mux.Handle("/", access)
	t.Cleanup(access.Close)
	return &mcpAccessFixture{access, oauth, queue, server}
}
func fixtureReceiverDescription() agentlink.Description {
	return agentlink.Description{Version: 1, Scope: "caller", Operations: []agentlink.Operation{agentlink.TaskSubmit, agentlink.TaskRead, agentlink.TaskCancel, agentlink.TaskInput, agentlink.TaskEvents}}
}
func fixtureRegistration() agentlink.MCPReceiverRegistration {
	return agentlink.MCPReceiverRegistration{Version: 1, Receiver: agentlink.Identity{Address: agentlink.NodeTarget("receiver-fixture"), Name: "Fixture receiver", Adapter: "fixture"}, Capabilities: fixtureReceiverDescription()}
}
func fixtureResponse(t *testing.T, response *http.Response, status int, destination any) {
	t.Helper()
	defer response.Body.Close()
	if response.StatusCode != status {
		raw, _ := io.ReadAll(response.Body)
		t.Fatalf("HTTP %d wanted %d: %s", response.StatusCode, status, raw)
	}
	if destination != nil {
		if e := json.NewDecoder(response.Body).Decode(destination); e != nil {
			t.Fatal(e)
		}
	}
}
func (f *mcpAccessFixture) request(t *testing.T, method, path, owner string, body any) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != nil {
		reader = strings.NewReader(mcpJSON(body))
	}
	r, e := http.NewRequest(method, f.http.URL+path, reader)
	if e != nil {
		t.Fatal(e)
	}
	if owner != "" {
		r.Header.Set("X-Fixture-Owner", owner)
	}
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", f.http.URL)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, e := client.Do(r)
	if e != nil {
		t.Fatal(e)
	}
	return response
}

type mcpFixtureGrant struct {
	Token      string `json:"access_token"`
	Refresh    string `json:"refresh_token"`
	Connection string `json:"connection_id"`
	Node       string `json:"node_id"`
	ClientID   string `json:"-"`
}

func (f *mcpAccessFixture) grant(t *testing.T, receiver bool) (grant mcpFixtureGrant) {
	t.Helper()
	client := mcpOAuthClient{RedirectURIs: []string{"http://127.0.0.1:9876/callback"}, ClientName: "Synthetic client"}
	if receiver {
		client.ReceiverNode = "receiver-fixture"
	}
	return f.grantClient(t, client)
}

func (f *mcpAccessFixture) grantClient(t *testing.T, client mcpOAuthClient) (grant mcpFixtureGrant) {
	t.Helper()
	var registration struct {
		ClientID                string   `json:"client_id"`
		GrantTypes              []string `json:"grant_types"`
		TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	}
	fixtureResponse(t, f.request(t, "POST", "/oauth/register", "", client), 201, &registration)
	if mcpJSON(registration.GrantTypes) != `["authorization_code"]` || registration.TokenEndpointAuthMethod != "none" {
		t.Fatal("registration did not select the supported public-client flow")
	}
	verifier := strings.Repeat("v", 43)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	values := url.Values{"client_id": {registration.ClientID}, "redirect_uri": {client.RedirectURIs[0]}, "resource": {f.http.URL + "/mcp"}, "response_type": {"code"}, "code_challenge_method": {"S256"}, "code_challenge": {challenge}, "state": {"original-state"}}
	response := f.request(t, "GET", "/oauth/authorize?"+values.Encode(), "owner-fixture", nil)
	if response.StatusCode != 303 {
		t.Fatalf("authorize: %d", response.StatusCode)
	}
	location, e := url.Parse(response.Header.Get("Location"))
	response.Body.Close()
	if e != nil {
		t.Fatal(e)
	}
	var decision struct {
		Redirect string `json:"redirect"`
	}
	fixtureResponse(t, f.request(t, "POST", "/oauth/consent", "owner-fixture", mcpConsentInput{RequestID: location.Query().Get("request"), Approve: true}), 200, &decision)
	callback, e := url.Parse(decision.Redirect)
	if e != nil {
		t.Fatal(e)
	}
	if callback.Query().Get("state") != "original-state" {
		t.Fatal("state lost")
	}
	values = url.Values{"grant_type": {"authorization_code"}, "code": {callback.Query().Get("code")}, "code_verifier": {verifier}, "client_id": {registration.ClientID}, "redirect_uri": {client.RedirectURIs[0]}, "resource": {f.http.URL + "/mcp"}}
	response, e = http.PostForm(f.http.URL+"/oauth/token", values)
	if e != nil {
		t.Fatal(e)
	}
	fixtureResponse(t, response, 200, &grant)
	if grant.Refresh != "" {
		t.Fatal("code-only flow issued a refresh credential")
	}
	grant.ClientID = registration.ClientID
	// Every issued code is single-use, including an otherwise exact token retry.
	response, e = http.PostForm(f.http.URL+"/oauth/token", values)
	if e != nil {
		t.Fatal(e)
	}
	fixtureResponse(t, response, 400, nil)
	return grant
}

type mcpBearerTransport struct {
	token string
	base  http.RoundTripper
}

func (t mcpBearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	copy := r.Clone(r.Context())
	copy.Header = r.Header.Clone()
	copy.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(copy)
}
func (f *mcpAccessFixture) session(t *testing.T, grant mcpFixtureGrant) *mcp.Session {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, e := mcp.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: f.http.URL + "/mcp", HTTPClient: &http.Client{Transport: mcpBearerTransport{grant.Token, http.DefaultTransport}}, MaxRetries: -1}, mcp.Options{})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func fixtureCall(t *testing.T, s *mcp.Session, name string, input, output any) {
	t.Helper()
	result, e := s.CallTool(context.Background(), name, json.RawMessage(mcpJSON(input)))
	if e != nil {
		t.Fatal(name, e)
	}
	if result.IsError {
		t.Fatalf("%s: %s", name, mcpJSON(result.StructuredContent))
	}
	if output != nil {
		if e = mcpRead(mcpJSON(result.StructuredContent), output); e != nil {
			t.Fatal(e)
		}
	}
}
func fixtureCallError(t *testing.T, s *mcp.Session, name string, input any, want agentlink.MCPErrorCode) {
	t.Helper()
	result, e := s.CallTool(context.Background(), name, json.RawMessage(mcpJSON(input)))
	if e != nil {
		t.Fatal(name, e)
	}
	var failure agentlink.MCPError
	if !result.IsError || mcpRead(mcpJSON(result.StructuredContent), &failure) != nil || failure.Code != want {
		t.Fatalf("%s expected %s: %s", name, want, mcpJSON(result))
	}
}
func TestMCPAccessHTTPIsolationAndReceiverLifecycle(t *testing.T) {
	f := mcpAccessTestFixture(t)
	ordinary := f.grant(t, false)
	other := f.grant(t, false)
	receiver := f.grant(t, true)
	if receiver.Node != "receiver-fixture" || ordinary.Node != "" {
		t.Fatal("receiver identity did not follow consent")
	}
	callerSession := f.session(t, ordinary)
	otherSession := f.session(t, other)
	receiverSession := f.session(t, receiver)
	var registration map[string]string
	fixtureCall(t, receiverSession, "pot_receiver_register", fixtureRegistration(), &registration)
	if registration["connection_id"] != receiver.Connection {
		t.Fatal(registration)
	}
	if result, e := callerSession.CallTool(context.Background(), "pot_receiver_register", json.RawMessage(mcpJSON(fixtureRegistration()))); e == nil && !result.IsError {
		t.Fatal("ordinary caller reached receiver registration")
	}
	tools, e := callerSession.ListTools(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	for _, tool := range tools {
		if strings.Contains(tool.Name, "owner") || strings.Contains(tool.Name, "approval") || strings.HasPrefix(tool.Name, "pot_receiver_") {
			t.Fatal("owner operation advertised")
		}
	}
	input := agentlink.MCPSubmitInput{RequestID: "submit", Target: agentlink.NodeTarget("receiver-fixture"), Text: "Run fixture"}
	var accepted MCPTaskAcceptance
	fixtureCall(t, callerSession, "pot_task_submit", input, &accepted)
	if accepted.Task.RunID != "" {
		t.Fatal("invented run before admission")
	}
	var retry MCPTaskAcceptance
	fixtureCall(t, callerSession, "pot_task_submit", input, &retry)
	if !retry.Receipt.Duplicate || retry.Task.ID != accepted.Task.ID {
		t.Fatal("duplicate dispatch")
	}
	conflict := input
	conflict.Text = "different"
	fixtureCallError(t, callerSession, "pot_task_submit", conflict, agentlink.MCPConflict)
	fixtureCallError(t, otherSession, "pot_task_read", map[string]string{"task_id": accepted.Task.ID}, agentlink.MCPUnavailable)
	var delivery MCPClaimedDelivery
	fixtureCall(t, receiverSession, "pot_receiver_claim", map[string]any{"request_id": "claim", "wait_ms": 0}, &delivery)
	var repeated MCPClaimedDelivery
	fixtureCall(t, receiverSession, "pot_receiver_claim", map[string]any{"request_id": "claim", "wait_ms": 0}, &repeated)
	if repeated.Claim != delivery.Claim {
		t.Fatal("claim changed on lost response retry")
	}
	ack := agentlink.MCPDeliveryAck{Claim: delivery.Claim, Accepted: true}
	fixtureCall(t, receiverSession, "pot_receiver_ack", ack, nil)
	fixtureCall(t, receiverSession, "pot_receiver_ack", ack, nil)
	binding := agentlink.MCPRunBinding{Task: protocol.AgentRef{NodeID: "receiver-fixture", Kind: "task", AgentID: "actual-task"}, RunID: "actual-run", Receiver: input.Target}
	event := agentlink.MCPExecutionEvent{Claim: delivery.Claim, Binding: binding, Sequence: 1, Phase: protocol.TaskRunning}
	fixtureCallError(t, receiverSession, "pot_receiver_event", event, agentlink.MCPFenced)
	admission := agentlink.MCPRunAdmission{Claim: delivery.Claim, Binding: binding}
	fixtureCall(t, receiverSession, "pot_receiver_admit", admission, nil)
	fixtureCall(t, receiverSession, "pot_receiver_admit", admission, nil)
	fixtureCall(t, receiverSession, "pot_receiver_event", event, nil)
	event.Sequence = 2
	event.Phase = protocol.TaskComplete
	event.Result = &protocol.TaskResultEvent{TaskID: binding.Task.AgentID, RunID: binding.RunID, Phase: event.Phase, Title: "Fixture", Text: "Actual result", Origin: protocol.TaskOrigin{Kind: "manual"}}
	var receipt agentlink.MCPEventReceipt
	fixtureCall(t, receiverSession, "pot_receiver_event", event, &receipt)
	fixtureCall(t, receiverSession, "pot_receiver_event", event, &receipt)
	if !receipt.Duplicate {
		t.Fatal("event not reconciled")
	}
	changed := event
	changed.Result = &protocol.TaskResultEvent{TaskID: binding.Task.AgentID, RunID: binding.RunID, Phase: event.Phase, Title: "Fixture", Text: "Conflict", Origin: protocol.TaskOrigin{Kind: "manual"}}
	fixtureCallError(t, receiverSession, "pot_receiver_event", changed, agentlink.MCPConflict)
	var task agentlink.Task
	fixtureCall(t, callerSession, "pot_task_read", map[string]string{"task_id": accepted.Task.ID}, &task)
	if task.State != "complete" || task.Result != "Actual result" || task.RunID != binding.RunID {
		t.Fatal(task)
	}
	// The catalogue and terminal outcome index commit with the event receipt.
	info, e := f.oauth.Verify(context.Background(), ordinary.Token, nil)
	if e != nil {
		t.Fatal(e)
	}
	caller := info.Extra["caller"].(agentlink.MCPCaller)
	record, e := f.queue.Tasks.Get(context.Background(), caller.Actor().AgentID, task.ID)
	if e != nil || record == nil || record.Execution == nil || record.Execution.Receipt.ID != binding.Task.AgentID || record.Info.Phase != protocol.TaskComplete {
		t.Fatal(record, e)
	}
	// Terminal continuation gets its own claim and a new real run, under old-run CAS.
	continuation := agentlink.MCPTaskInput{RequestID: "continue", TaskID: task.ID, RunID: binding.RunID, Text: "Continue"}
	fixtureCall(t, callerSession, "pot_task_input", continuation, nil)
	var control MCPClaimedDelivery
	fixtureCall(t, receiverSession, "pot_receiver_claim", map[string]any{"request_id": "control", "wait_ms": 0}, &control)
	if control.Control == nil || control.Control.Binding != binding {
		t.Fatal(control)
	}
	fixtureCall(t, receiverSession, "pot_receiver_ack", agentlink.MCPDeliveryAck{Claim: control.Claim, Accepted: true}, nil)
	next := binding
	next.RunID = "next-real-run"
	fixtureCallError(t, receiverSession, "pot_receiver_admit", agentlink.MCPRunAdmission{Claim: control.Claim, Binding: next, PreviousRunID: "wrong"}, agentlink.MCPFenced)
	fixtureCall(t, receiverSession, "pot_receiver_admit", agentlink.MCPRunAdmission{Claim: control.Claim, Binding: next, PreviousRunID: binding.RunID}, nil)
	fixtureCallError(t, receiverSession, "pot_receiver_event", event, agentlink.MCPFenced)
	oldRun, e := f.queue.Tasks.Run(context.Background(), caller.Actor().AgentID, task.ID, binding.RunID)
	if e != nil || oldRun == nil || oldRun.Outcome == nil || oldRun.Outcome.Text != "Actual result" || oldRun.RunID != binding.RunID {
		t.Fatal("continuation erased prior execution", oldRun, e)
	}
	cancel := agentlink.MCPCancelInput{RequestID: "cancel", TaskID: task.ID, RunID: next.RunID}
	fixtureCall(t, callerSession, "pot_task_cancel", cancel, nil)
	fixtureCall(t, callerSession, "pot_task_read", map[string]string{"task_id": task.ID}, &task)
	if task.State != "queued" {
		t.Fatal("cancellation falsely terminal", task)
	}
	// Revocation invalidates every HTTP operation, even an established SDK session.
	if e = f.oauth.Revoke(context.Background(), ordinary.Connection); e != nil {
		t.Fatal(e)
	}
	if _, e = callerSession.ListTools(context.Background()); e == nil {
		t.Fatal("revoked SDK session accepted")
	}
}
func TestMCPAccessOAuthValidationAndExpiry(t *testing.T) {
	f := mcpAccessTestFixture(t)
	response := f.request(t, "POST", "/mcp", "", map[string]any{})
	if response.StatusCode != 401 || !strings.Contains(response.Header.Get("WWW-Authenticate"), "oauth-protected-resource/mcp") {
		t.Fatal("missing resource discovery challenge", response.StatusCode)
	}
	response.Body.Close()
	var discovery map[string]any
	fixtureResponse(t, f.request(t, "GET", "/.well-known/oauth-authorization-server", "", nil), 200, &discovery)
	if mcpJSON(discovery["grant_types_supported"]) != `["authorization_code"]` {
		t.Fatal(discovery)
	}
	for _, bad := range []mcpOAuthClient{{RedirectURIs: []string{"http://remote.example/callback"}}, {RedirectURIs: []string{"https://client.example/callback"}, GrantTypes: []string{"implicit"}}, {RedirectURIs: []string{"https://client.example/callback"}, TokenEndpointAuthMethod: "client_secret_basic"}} {
		fixtureResponse(t, f.request(t, "POST", "/oauth/register", "", bad), 400, nil)
	}
	grant := f.grant(t, false)
	if _, e := f.oauth.Verify(context.Background(), grant.Token, nil); e != nil {
		t.Fatal(e)
	}
	if _, e := f.oauth.options.DB.Exec("UPDATE mcp_oauth_tokens SET expires_at=? WHERE connection_id=?", time.Now().Add(-time.Second).Unix(), grant.Connection); e != nil {
		t.Fatal(e)
	}
	if _, e := f.oauth.Verify(context.Background(), grant.Token, nil); e == nil {
		t.Fatal("expired access accepted")
	}
	if _, e := f.oauth.options.DB.Exec("UPDATE mcp_oauth_tokens SET expires_at=?,resource='https://wrong.example/mcp' WHERE connection_id=?", time.Now().Add(time.Hour).Unix(), grant.Connection); e != nil {
		t.Fatal(e)
	}
	if _, e := f.oauth.Verify(context.Background(), grant.Token, nil); e == nil {
		t.Fatal("wrong audience accepted")
	}
	// Owner browser session is not an MCP bearer and cannot expose the owner router.
	fixtureResponse(t, f.request(t, "POST", "/mcp", "owner-fixture", map[string]any{}), 401, nil)
	fixtureResponse(t, f.request(t, "GET", "/control/v1/owner", "owner-fixture", nil), 404, nil)
	fixtureResponse(t, f.request(t, "GET", "/oauth/connections", "", nil), 401, nil)
}

func TestMCPAccessOAuthNegotiatesAuthorizationCodeWithoutRefresh(t *testing.T) {
	f := mcpAccessTestFixture(t)
	// Codex 0.163.0's pinned rmcp requests both grants even when discovery
	// advertises authorization_code only. RFC 7591 section 3.2.1 permits the
	// server to replace requested metadata and return the selected values.
	for _, grants := range [][]string{{"authorization_code", "refresh_token"}, {"refresh_token", "authorization_code"}} {
		grant := f.grantClient(t, mcpOAuthClient{
			ClientName:              "Codex",
			RedirectURIs:            []string{"http://127.0.0.1:9876/callback/synthetic-client"},
			GrantTypes:              grants,
			ResponseTypes:           []string{"code"},
			TokenEndpointAuthMethod: "none",
		})
		stored, err := f.oauth.client(context.Background(), grant.ClientID)
		if err != nil || mcpJSON(stored.GrantTypes) != `["authorization_code"]` {
			t.Fatal("unsupported refresh grant was persisted", err)
		}
		session := f.session(t, grant)
		var directory map[string]any
		fixtureCall(t, session, "pot_directory", map[string]any{}, &directory)
		response, err := http.PostForm(f.http.URL+"/oauth/token", url.Values{"grant_type": {"refresh_token"}, "client_id": {grant.ClientID}, "refresh_token": {"synthetic-not-issued"}})
		if err != nil {
			t.Fatal(err)
		}
		fixtureResponse(t, response, 400, nil)
	}
	for _, grants := range [][]string{{"refresh_token"}, {"implicit"}, {"authorization_code", "password"}, {"authorization_code", "client_credentials"}, {"authorization_code", "refresh_token", "unknown"}, {"authorization_code", "authorization_code"}, {"authorization_code", "refresh_token", "refresh_token"}} {
		client := mcpOAuthClient{RedirectURIs: []string{"http://127.0.0.1:9876/callback"}, GrantTypes: grants, TokenEndpointAuthMethod: "none"}
		fixtureResponse(t, f.request(t, "POST", "/oauth/register", "", client), 400, nil)
	}
	for _, client := range []mcpOAuthClient{
		{RedirectURIs: []string{"http://remote.example/callback"}, GrantTypes: []string{"authorization_code", "refresh_token"}},
		{RedirectURIs: []string{"https://client.example/callback"}, GrantTypes: []string{"authorization_code", "refresh_token"}, TokenEndpointAuthMethod: "client_secret_basic"},
	} {
		fixtureResponse(t, f.request(t, "POST", "/oauth/register", "", client), 400, nil)
	}
}
func TestMCPAccessQueueRestartAndOwnership(t *testing.T) {
	f := mcpAccessTestFixture(t)
	ctx := context.Background()
	grant := f.grant(t, true)
	other := f.grant(t, false)
	ti, e := f.oauth.Verify(ctx, other.Token, nil)
	if e != nil {
		t.Fatal(e)
	}
	caller := ti.Extra["caller"].(agentlink.MCPCaller)
	receiver, e := agentlink.NewMCPReceiver(grant.Connection, grant.Node)
	if e != nil {
		t.Fatal(e)
	}
	if e = f.queue.Register(ctx, receiver, fixtureRegistration(), fixtureReceiverDescription()); e != nil {
		t.Fatal(e)
	}
	input := agentlink.MCPSubmitInput{RequestID: "durable", Target: agentlink.NodeTarget(grant.Node), Text: "Work"}
	accepted, e := f.queue.Submit(ctx, caller, input)
	if e != nil {
		t.Fatal(e)
	}
	delivery, e := f.queue.Claim(ctx, receiver, "stable-claim", 0)
	if e != nil || delivery == nil {
		t.Fatal(delivery, e)
	}
	if e = f.queue.Ack(ctx, receiver, agentlink.MCPDeliveryAck{Claim: delivery.Claim, Accepted: true}); e != nil {
		t.Fatal(e)
	}
	// Restart object, preserving the caller-owned database. No claim expiry/reassignment.
	reopened := &MCPAccessQueue{DB: f.queue.DB, Tasks: f.queue.Tasks, NodeID: f.queue.NodeID}
	same, e := reopened.Claim(ctx, receiver, "stable-claim", 0)
	if e != nil || same.Claim != delivery.Claim {
		t.Fatal(same, e)
	}
	none, e := reopened.Claim(ctx, receiver, "new-claim", 0)
	if e != nil || none != nil {
		t.Fatal("accepted work reassigned", none, e)
	}
	forged, _ := agentlink.NewMCPReceiver("different-connection", receiver.NodeID())
	if e = reopened.Ack(ctx, forged, agentlink.MCPDeliveryAck{Claim: delivery.Claim, Accepted: true}); !errors.Is(e, agentlink.ErrUnauthorized) {
		t.Fatal("foreign ack accepted", e)
	}
	// Exact accepted retry remains available after the endpoint is retired.
	if _, e = f.queue.DB.Exec("DELETE FROM mcp_receiver_endpoints"); e != nil {
		t.Fatal(e)
	}
	retry, e := reopened.Submit(ctx, caller, input)
	if e != nil || !retry.Receipt.Duplicate || retry.Task.ID != accepted.Task.ID {
		t.Fatal(retry, e)
	}
	var count int
	if e = f.queue.DB.QueryRow("SELECT count(*) FROM tasks").Scan(&count); e != nil || count != 1 {
		t.Fatal("duplicate catalogue task", count, e)
	}
}
