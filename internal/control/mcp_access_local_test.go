package control

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/protocol"
)

func mcpLocalAccessFixture(t *testing.T) *mcpAccessFixture {
	t.Helper()
	backend, _ := linkBackendFixture(t)
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	access, e := backend.s.NewMCPAccess(MCPOAuthOptions{Origin: server.URL, LoginURL: "/mcp-connect.html", OwnerSession: func(r *http.Request) (string, bool) {
		return r.Header.Get("X-Fixture-Owner"), r.Header.Get("X-Fixture-Owner") != ""
	}}, nil)
	if e != nil {
		t.Fatal(e)
	}
	mux.Handle("/", access)
	t.Cleanup(access.Close)
	return &mcpAccessFixture{access: access, oauth: access.options.OAuth, queue: access.options.Queue, http: server}
}
func TestMCPAccessLocalPotBusinessProjection(t *testing.T) {
	f := mcpLocalAccessFixture(t)
	ctx := context.Background()
	grant := f.grant(t, false)
	other := f.grant(t, false)
	session := f.session(t, grant)
	otherSession := f.session(t, other)
	var directory agentlink.Directory
	fixtureCall(t, session, "pot_directory", map[string]any{}, &directory)
	if len(directory.Targets) != 2 {
		t.Fatal(directory)
	}
	for _, endpoint := range directory.Targets {
		for _, op := range endpoint.Capabilities.Operations {
			if op != agentlink.TaskSubmit && op != agentlink.TaskRead && op != agentlink.TaskCancel && op != agentlink.ChatSend && op != agentlink.ChatRead && op != agentlink.ChatReceipt {
				t.Fatal("owner/unsupported capability", op)
			}
		}
	}
	target := agentlink.AssistantTarget(f.queue.NodeID, "main")
	message := MCPAccessMessageInput{RequestID: "message", Message: protocol.SendMail{To: target, Text: "Reference only, never human approval"}}
	var mail MCPAccessMessageAcceptance
	fixtureCall(t, session, "pot_message_send", message, &mail)
	if mail.Message.Message.From.Kind != "user" || mail.Message.Message.Owner != "" || mail.Message.Message.Kind != "message" || mail.Message.Message.To != target || mail.Message.Message.From.AgentID == "main" {
		t.Fatal("message gained authority", mail)
	}
	// A real mailbox reply retains the original conversation/reply/hop causation.
	if _, e := f.queue.DB.Exec("UPDATE agent_messages SET state='processing' WHERE id=?", mail.Message.Message.ID); e != nil {
		t.Fatal(e)
	}
	reply, e := f.access.options.Local.Mailbox.Send(ctx, "fixture-reply", target, protocol.SendMail{To: mail.Message.Message.From, Text: "Bound reply"}, &mail.Message.Message)
	if e != nil {
		t.Fatal(e)
	}
	var page MCPAccessMessagePage
	fixtureCall(t, session, "pot_message_read", MCPAccessMessageRead{MessageID: mail.Message.Message.ID}, &page)
	if len(page.Messages) != 2 || page.Messages[1].Message.ID != reply.ID || reply.ReplyTo == nil || *reply.ReplyTo != mail.Message.Message.ID || reply.Hops != 1 {
		t.Fatal("reply causation lost", page)
	}
	fixtureCallError(t, otherSession, "pot_message_read", MCPAccessMessageRead{MessageID: mail.Message.Message.ID}, agentlink.MCPUnavailable)
	var repeated MCPAccessMessageAcceptance
	fixtureCall(t, session, "pot_message_send", message, &repeated)
	if !repeated.Receipt.Duplicate || mcpJSON(mail.Message) != mcpJSON(repeated.Message) {
		t.Fatal("message retry changed causation")
	}
	conflict := message
	conflict.Message.Text = "different"
	fixtureCallError(t, session, "pot_message_send", conflict, agentlink.MCPConflict)
	submit := agentlink.MCPSubmitInput{RequestID: "local-task", Target: target, Text: "Local task\nDo not execute in this admission fixture"}
	var accepted MCPTaskAcceptance
	fixtureCall(t, session, "pot_task_submit", submit, &accepted)
	if accepted.Task.RunID == "" {
		t.Fatal("real local admission omitted run")
	}
	var owner, localID, payload string
	e = f.access.options.Local.integrationDB.QueryRowContext(ctx, "SELECT assistant_id,task_id,payload FROM agent_link_received_tasks WHERE id=?", accepted.Task.ID).Scan(&owner, &localID, &payload)
	if e != nil {
		t.Fatal(e)
	}
	record, e := f.queue.Tasks.Get(ctx, owner, localID)
	if e != nil || record == nil || record.RunID != accepted.Task.RunID {
		t.Fatal(record, e)
	}
	var request agentlink.TaskRequest
	if mcpRead(payload, &request) != nil || request.From != mail.Message.Message.From || request.From.Kind != "user" || request.Configuration != nil || request.Workspace != "" {
		t.Fatal("local submit lost caller scope", payload)
	}
	fixtureCallError(t, otherSession, "pot_task_read", map[string]string{"task_id": accepted.Task.ID}, agentlink.MCPUnavailable)
	// Lost final response: retry may read the original business receipt, never dispatch again.
	if _, e = f.queue.DB.Exec("UPDATE mcp_local_mutations SET state='pending',receipt='' WHERE operation=? AND request_id=?", agentlink.TaskSubmit, submit.RequestID); e != nil {
		t.Fatal(e)
	}
	var retry MCPTaskAcceptance
	fixtureCall(t, session, "pot_task_submit", submit, &retry)
	if !retry.Receipt.Duplicate || retry.Task.ID != accepted.Task.ID {
		t.Fatal("local retry was redispatched", retry)
	}
	fixtureCallError(t, session, "pot_task_cancel", agentlink.MCPCancelInput{RequestID: "wrong-run", TaskID: accepted.Task.ID, RunID: "forged-run"}, agentlink.MCPFenced)
	record, e = f.queue.Tasks.Get(ctx, owner, localID)
	if e != nil || record.Info.Phase != protocol.TaskQueued {
		t.Fatal("wrong run cancelled", record, e)
	}
	var cancelled MCPTaskAcceptance
	fixtureCall(t, session, "pot_task_cancel", agentlink.MCPCancelInput{RequestID: "cancel", TaskID: accepted.Task.ID, RunID: accepted.Task.RunID}, &cancelled)
	if cancelled.Task.RunID != accepted.Task.RunID || cancelled.Task.State != "cancelled" {
		t.Fatal("pre-dispatch cancellation missing durable evidence", cancelled)
	}
	var count int
	if e = f.queue.DB.QueryRow("SELECT count(*) FROM tasks").Scan(&count); e != nil || count != 1 {
		t.Fatal("local retry created new task", count, e)
	}
	// Removing a target cannot erase the immutable accepted message receipt.
	if e = f.access.options.Local.Registry.Delete(ctx, "main", currentAssistantRevision(t, f.access.options.Local, "main")); e != nil {
		t.Fatal(e)
	}
	fixtureCall(t, session, "pot_message_send", message, &repeated)
	if !repeated.Receipt.Duplicate {
		t.Fatal("historical receipt lost")
	}
}
func TestMCPAccessUncertainLocalMutationOnlyReconciles(t *testing.T) {
	f := mcpLocalAccessFixture(t)
	ctx := context.Background()
	grant := f.grant(t, false)
	info, e := f.oauth.Verify(ctx, grant.Token, nil)
	if e != nil {
		t.Fatal(e)
	}
	caller := info.Extra["caller"].(agentlink.MCPCaller)
	input := agentlink.MCPSubmitInput{RequestID: "uncertain", Target: agentlink.AssistantTarget(f.queue.NodeID, "main"), Text: "Uncertain"}
	digest, e := input.Digest(caller)
	if e != nil {
		t.Fatal(e)
	}
	dispatches, reconciles := 0, 0
	dispatch := func() (agentlink.Task, error) {
		dispatches++
		return agentlink.Task{}, errors.New("synthetic lost effect response")
	}
	reconcile := func() (agentlink.Task, error) { reconciles++; return agentlink.Task{}, agentlink.ErrUnavailable }
	for index := 0; index < 3; index++ {
		_, e = f.access.localMutation(ctx, caller, agentlink.TaskSubmit, input.RequestID, digest, input, dispatch, reconcile)
		if mcpFailure(e).Code != agentlink.MCPUnknown {
			t.Fatal("uncertainty permitted retry", e)
		}
	}
	if dispatches != 1 || reconciles != 2 {
		t.Fatal("uncertain execution replayed", dispatches, reconciles)
	}
	_, e = f.access.localMutation(ctx, caller, agentlink.TaskSubmit, input.RequestID, "different-digest", input, dispatch, reconcile)
	if !errors.Is(e, agentlink.ErrConflict) || dispatches != 1 {
		t.Fatal("conflicting retry dispatched", e)
	}
}

func TestMCPDirectoryDoesNotSelectLegacyDefault(t *testing.T) {
	f := mcpLocalAccessFixture(t)
	ctx := context.Background()
	var before int
	if e := f.access.options.Local.integrationDB.QueryRow("SELECT count(*) FROM agent_link_node_settings").Scan(&before); e != nil || before != 0 {
		t.Fatal(before, e)
	}
	session := f.session(t, f.grant(t, false))
	var directory agentlink.Directory
	fixtureCall(t, session, "pot_directory", map[string]any{}, &directory)
	if directory.DefaultTarget != nil || len(directory.Targets) != 2 {
		t.Fatal("explicit MCP catalogue selected a default", directory)
	}
	var after int
	if e := f.access.options.Local.integrationDB.QueryRow("SELECT count(*) FROM agent_link_node_settings").Scan(&after); e != nil || after != 0 {
		t.Fatal("MCP read initialized legacy settings", after, e)
	}
	_ = ctx
}
