package control

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/pkg/agentlink"

	"github.com/jjyr/pot/pkg/protocol"
	"net/http"
	"testing"
	"time"
)

// Synthetic review reproducer; no native command is executed.
func TestMCPReverseHumanRejectsUnassignedSameNodeUser(t *testing.T) {
	a, _ := potMCPControl(t, "review-a")
	b, _ := potMCPControl(t, "review-b")
	targetAssistant, err := a.Registry.Create(t.Context(), "other", "Other")
	if err != nil {
		t.Fatal(err)
	}
	server := potMCPServer(t, a)
	c := potMCPAuthorize(t, b, server, "")
	client := potMCPClient(t, b, c.ID)
	potMCPExposeClient(t, b, c.ID, client)
	ctx := t.Context()
	target := agentlink.AssistantTarget(a.options.RuntimeID, targetAssistant.ID)
	owner := "agent-link:" + c.ID + ":main"
	req, _ := json.Marshal(protocol.CreateTask{RequestID: "review-human", Text: "synthetic review", Target: &target})
	value, err := b.CallAgent(ctx, owner, agentlink.TaskSubmit, agentlink.Call{Body: req})
	if err != nil {
		t.Fatal(err)
	}
	info := value.(protocol.TaskInfo)
	if err != nil {
		t.Fatal(err)
	}
	b.syncTaskExecutions(ctx)
	record, err := b.Tasks.Get(ctx, linkChatID(c.ID, "main"), info.ID)
	if err != nil || record == nil {
		t.Fatal(err)
	}
	if err = a.Tasks.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	b.syncTaskExecutions(ctx)
	var actualTask string
	if err = a.integrationDB.QueryRow("SELECT task_id FROM agent_link_received_tasks WHERE id=?", record.Execution.Receipt.ID).Scan(&actualTask); err != nil {
		t.Fatal(err)
	}
	actual, err := a.Tasks.Get(ctx, targetAssistant.ID, actualTask)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Permissions.Enroll("machine", "epoch"); err != nil {
		t.Fatal(err)
	}
	host := hostRequest("synthetic-review-call")
	host.Owner = targetAssistant.ID
	host.Task = actualTask
	host.Turn = actual.RunID
	host.Generation = actual.Generation
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, e := a.Permissions.authorize(ctx, host, func() error { close(ready); return nil })
		done <- e
	}()
	select {
	case <-ready:
	case <-time.After(2 * time.Second):
		t.Fatal("pending absent")
	}
	reviews := b.outboundPotMCPReviews(ctx, owner)
	if len(reviews) != 1 {
		t.Fatalf("reviews=%d", len(reviews))
	}
	r := reviews[0].review
	original := record.Execution.Request.From
	if original == r.Caller || original.AgentID == linkChatID(c.ID, targetAssistant.ID) {
		t.Fatal("fixture must preserve distinct OAuth caller and source human/UI target", original, r.Caller)
	}
	submit := agentlink.MCPHumanTaskSubmit{User: original, Input: agentlink.MCPSubmitInput{RequestID: "review-human", Target: target, Text: "synthetic review"}}
	var retry MCPTaskAcceptance
	if err = client.Call(ctx, "pot_receiver_user_task_submit", submit, &retry); err != nil || !retry.Receipt.Duplicate || retry.Task.ID != record.Execution.Receipt.ID {
		t.Fatal("lost-response retry lost original receipt", retry, err)
	}
	conflict := submit
	conflict.User.AgentID = "different-unassigned-human"
	if err = client.Call(ctx, "pot_receiver_user_task_submit", conflict, &retry); err == nil {
		t.Fatal("conflicting human overwrote original")
	}
	conflict = submit
	conflict.Input.Target.AgentID = "main"
	if err = client.Call(ctx, "pot_receiver_user_task_submit", conflict, &retry); err == nil {
		t.Fatal("conflicting target overwrote original")
	}
	ordinary := server.session(t, server.grant(t, false))
	for _, name := range []string{"pot_receiver_user_task_submit", "pot_receiver_human_review_decide"} {
		result, callErr := ordinary.CallTool(ctx, name, json.RawMessage(mcpJSON(submit)))
		if callErr == nil && !result.IsError {
			t.Fatal("ordinary endpoint accepted receiver metadata", name, result)
		}
	}

	other := original
	other.AgentID = "different-unassigned-human"
	in := agentlink.MCPHumanReviewDecision{Caller: r.Caller, RequestID: r.RequestID, TaskID: r.TaskID, ExecutionTask: r.ExecutionTask, RunID: r.RunID, Generation: r.Generation, Epoch: r.Epoch, ApprovalID: r.Approval.ID, User: other, Action: "allow_once"}
	var out struct {
		Accepted bool `json:"accepted"`
	}
	if err = client.Call(ctx, "pot_receiver_human_review_decide", in, &out); err == nil {
		t.Fatal("unassigned human accepted")
	}
	select {
	case err = <-done:
		t.Fatal("invalid decision consumed pending", err)
	default:
	}
	if len(a.Permissions.Pending(targetAssistant.ID)) != 1 {
		t.Fatal("real pending was removed")
	}
	body, _ := json.Marshal(map[string]string{"id": potMCPOutboundApprovalID(c, r), "action": "allow_once"})
	if _, err = b.ManageApproval(ctx, owner, body); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("not consumed")
	}
	// Upgrade from a submission without original provenance never adds guessed
	// authority, even for the otherwise correct original human and exact retry.
	if _, err = a.Tasks.DB().Exec("DELETE FROM mcp_task_humans WHERE request_id=?", submit.Input.RequestID); err != nil {
		t.Fatal(err)
	}
	if err = client.Call(ctx, "pot_receiver_user_task_submit", submit, &retry); err == nil {
		t.Fatal("old submission acquired new human authority")
	}
	legacyCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	host.CallID = "legacy-pending"
	legacyReady := make(chan struct{})
	legacyDone := make(chan error, 1)
	go func() {
		_, err := a.Permissions.authorize(legacyCtx, host, func() error { close(legacyReady); return nil })
		legacyDone <- err
	}()
	select {
	case <-legacyReady:
	case <-time.After(2 * time.Second):
		t.Fatal("legacy pending absent")
	}
	legacy := a.Permissions.Pending(targetAssistant.ID)
	if len(legacy) != 1 {
		t.Fatal(legacy)
	}
	in.User = original
	in.ApprovalID = legacy[0].ID
	if err = client.Call(ctx, "pot_receiver_human_review_decide", in, &out); err == nil {
		t.Fatal("missing provenance approved real pending")
	}
	select {
	case err = <-legacyDone:
		t.Fatal("legacy pending consumed", err)
	default:
	}
	cancel()
	<-legacyDone
	t.Log("unassigned same-node human rejected; original ManageApproval resolved the real pending once without rules/grants")
}

func TestMCPRejectsReceiverInLocalNodeNamespace(t *testing.T) {
	a, _ := potMCPControl(t, "local-protected-node")
	f := potMCPServer(t, a)
	f.access.options.ReceiverCapabilities = publishedReceiverCapabilities
	redirect := "http://127.0.0.1:9876/callback"
	fixtureResponse(t, f.request(t, "POST", "/oauth/register", "", mcpOAuthClient{RedirectURIs: []string{redirect}, ReceiverNode: f.queue.NodeID}), 400, nil)
	// A persisted client/pending request from the buggy source cannot be consented.
	client := f.registerClient(t, redirect)
	pending := f.pending(t, client, redirect)
	if _, err := f.oauth.options.DB.Exec("UPDATE mcp_oauth_clients SET metadata=json_set(metadata,'$.pot_receiver_node',?) WHERE id=?", f.queue.NodeID, client); err != nil {
		t.Fatal(err)
	}
	fixtureResponse(t, f.request(t, "GET", "/oauth/authorize?"+f.authorizationValues(client, redirect).Encode(), "owner-one", nil), 400, nil)
	fixtureResponse(t, f.request(t, "GET", "/oauth/consent?request="+pending, "owner-one", nil), 400, nil)
	fixtureResponse(t, f.request(t, "POST", "/oauth/consent", "owner-one", mcpConsentInput{RequestID: pending, Approve: true}), 400, nil)
	// Existing grants and SDK sessions are checked again; no history is deleted.
	grant := f.grant(t, true)
	session := f.session(t, grant)
	if _, err := f.oauth.options.DB.Exec("UPDATE mcp_oauth_connections SET receiver_node=? WHERE id=?", f.queue.NodeID, grant.Connection); err != nil {
		t.Fatal(err)
	}
	if _, err := f.oauth.Verify(t.Context(), grant.Token, nil); err == nil {
		t.Fatal("old self-node bearer survived")
	}
	if err := f.oauth.CheckConnection(t.Context(), grant.Connection); !errors.Is(err, agentlink.ErrUnauthorized) {
		t.Fatal(err)
	}
	target := agentlink.AssistantTarget(f.queue.NodeID, "main")
	raw, _ := json.Marshal(agentlink.MCPNodeRead{Actor: target, TaskID: "local-private-task"})
	result, err := session.CallTool(t.Context(), "pot_receiver_node_task_get", raw)
	if err == nil {
		t.Fatal("old self-node SDK session accepted", result)
	}
	identity, _ := agentlink.NewMCPReceiver(grant.Connection, f.queue.NodeID)
	reg := agentlink.MCPReceiverRegistration{Version: 1, Receiver: agentlink.Identity{Address: target, Name: "forged local assistant"}, Capabilities: agentlink.Description{Version: 1, Scope: "peer", Operations: []agentlink.Operation{agentlink.TaskRead}}, Epoch: "forged-epoch"}
	// Queue admission also rejects the namespace with no OAuth callback installed.
	q := &MCPAccessQueue{DB: f.queue.DB, Tasks: f.queue.Tasks, NodeID: f.queue.NodeID}
	if err := q.Register(t.Context(), identity, reg, reg.Capabilities); !errors.Is(err, agentlink.ErrUnauthorized) {
		t.Fatal(err)
	}
}

func TestMCPOAuthRejectsStoredSelfNodeCode(t *testing.T) {
	f := mcpAccessTestFixture(t)
	redirect := "https://client.example/callback"
	client := f.registerClient(t, redirect)
	code := f.consentCode(t, client, redirect)
	if _, err := f.oauth.options.DB.Exec("UPDATE mcp_oauth_connections SET receiver_node=? WHERE client_id=?", f.queue.NodeID, client); err != nil {
		t.Fatal(err)
	}
	response, err := http.PostForm(f.http.URL+"/oauth/token", f.tokenValues(client, redirect, code))
	if err != nil {
		t.Fatal(err)
	}
	fixtureResponse(t, response, 400, nil)
}
