package control

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/mcp"
	"github.com/jjyr/pot/pkg/protocol"
	"testing"
	"time"
)

type gatewayFixture struct {
	f                *mcpAccessFixture
	receiver, caller *mcp.Session
	identity         agentlink.MCPReceiver
	claim            agentlink.MCPClaim
	binding          agentlink.MCPRunBinding
	execution        agentlink.MCPExecutionContext
	origin           protocol.AgentRef
	task             agentlink.Task
}

func newGatewayFixture(t *testing.T) *gatewayFixture { return gatewayFixtureFor(t, false) }
func gatewayFixtureFor(t *testing.T, assistant bool) *gatewayFixture {
	t.Helper()
	f := mcpLocalAccessFixture(t)
	description := fixtureReceiverDescription()
	description.Operations = append(description.Operations, agentlink.Approvals)
	f.access.options.ReceiverCapabilities = func(context.Context, agentlink.MCPReceiver, protocol.AgentRef) (agentlink.Description, error) {
		return description, nil
	}
	rg := f.grant(t, true)
	cg := f.grant(t, false)
	receiver := f.session(t, rg)
	caller := f.session(t, cg)
	registration := fixtureRegistration()
	registration.Capabilities = description
	registration.Epoch = "native-epoch"
	if assistant {
		registration.Receiver.Address = agentlink.AssistantTarget("receiver-fixture", "native-assistant")
		registration.Capabilities = agentlink.Description{Version: 1, Scope: "peer", Operations: []agentlink.Operation{agentlink.ChatSend, agentlink.ChatRead, agentlink.ChatReceipt}}
		f.access.options.ReceiverCapabilities = func(context.Context, agentlink.MCPReceiver, protocol.AgentRef) (agentlink.Description, error) {
			return registration.Capabilities, nil
		}
	}
	var registered any
	fixtureCall(t, receiver, "pot_receiver_register", registration, &registered)
	var acceptance MCPTaskAcceptance
	if assistant {
		var mail MCPAccessMessageAcceptance
		fixtureCall(t, caller, "pot_message_send", MCPAccessMessageInput{RequestID: "origin", Message: protocol.SendMail{To: registration.Receiver.Address, Text: "fixture"}}, &mail)
	} else {
		fixtureCall(t, caller, "pot_task_submit", agentlink.MCPSubmitInput{RequestID: "origin", Target: registration.Receiver.Address, Text: "fixture"}, &acceptance)
	}
	var delivery MCPClaimedDelivery
	fixtureCall(t, receiver, "pot_receiver_claim", map[string]any{"request_id": "claim", "wait_ms": 0}, &delivery)
	var ack agentlink.MCPDeliveryAck
	fixtureCall(t, receiver, "pot_receiver_ack", agentlink.MCPDeliveryAck{Claim: delivery.Claim, Accepted: true}, &ack)
	binding := agentlink.MCPRunBinding{Task: protocol.AgentRef{NodeID: rg.Node, Kind: "task", AgentID: "actual-native-task"}, RunID: "actual-native-run", Receiver: registration.Receiver.Address}
	var admission agentlink.MCPRunAdmission
	fixtureCall(t, receiver, "pot_receiver_admit", agentlink.MCPRunAdmission{Claim: delivery.Claim, Binding: binding}, &admission)
	var receipt agentlink.MCPEventReceipt
	fixtureCall(t, receiver, "pot_receiver_event", agentlink.MCPExecutionEvent{Claim: delivery.Claim, Binding: binding, Sequence: 1, Phase: protocol.TaskRunning}, &receipt)
	identity, _ := agentlink.NewMCPReceiver(rg.Connection, rg.Node)
	actor := binding.Task
	if assistant {
		actor = binding.Receiver
	}
	execution := agentlink.MCPExecutionContext{Claim: delivery.Claim, Binding: agentlink.MCPExecutionBinding{Actor: actor, Task: binding.Task, RunID: binding.RunID}}
	return &gatewayFixture{f, receiver, caller, identity, delivery.Claim, binding, execution, delivery.Work.Request.From, acceptance.Task}
}
func TestMCPExecutionGatewayHTTPReverseTaskMessageAndSpoofing(t *testing.T) {
	g := gatewayFixtureFor(t, true)
	ctx := context.Background()
	target := agentlink.AssistantTarget(g.f.queue.NodeID, "main")
	input := agentlink.MCPExecutionSubmit{Context: g.execution, Input: agentlink.MCPSubmitInput{RequestID: "reverse-task", Target: target, Text: "reverse local real task admission"}}
	var accepted MCPTaskAcceptance
	fixtureCall(t, g.receiver, "pot_receiver_task_submit", input, &accepted)
	if accepted.Task.RunID == "" {
		t.Fatal("missing real local run")
	}
	var raw string
	if e := g.f.access.options.Local.integrationDB.QueryRow("SELECT payload FROM agent_link_received_tasks WHERE id=?", accepted.Task.ID).Scan(&raw); e != nil {
		t.Fatal(e)
	}
	var request agentlink.TaskRequest
	if json.Unmarshal([]byte(raw), &request) != nil || request.From != g.execution.Binding.Actor || request.From == g.origin {
		t.Fatal("execution actor replaced by origin", raw)
	}
	var duplicate MCPTaskAcceptance
	fixtureCall(t, g.receiver, "pot_receiver_task_submit", input, &duplicate)
	if !duplicate.Receipt.Duplicate || duplicate.Task.ID != accepted.Task.ID {
		t.Fatal("reverse task replayed", duplicate)
	}
	var read agentlink.Task
	fixtureCall(t, g.receiver, "pot_receiver_task_read", agentlink.MCPExecutionRead{Context: g.execution, TaskID: accepted.Task.ID}, &read)
	message := agentlink.MCPExecutionMessage{Context: g.execution, Input: agentlink.MCPMessageInput{RequestID: "reverse-message", Message: protocol.SendMail{To: target, Text: "reference only"}}}
	var mail MCPAccessMessageAcceptance
	fixtureCall(t, g.receiver, "pot_receiver_message", message, &mail)
	if mail.Message.Message.From != g.execution.Binding.Actor || mail.Message.Message.Owner != "" || mail.Message.Message.Kind != "message" {
		t.Fatal("message authority corrupted", mail)
	}
	var repeated MCPAccessMessageAcceptance
	fixtureCall(t, g.receiver, "pot_receiver_message", message, &repeated)
	if !repeated.Receipt.Duplicate || mcpJSON(repeated.Message) != mcpJSON(mail.Message) {
		t.Fatal("mail replayed")
	}
	for _, mutate := range []func(*agentlink.MCPExecutionContext){func(c *agentlink.MCPExecutionContext) { c.Binding.Actor = g.origin }, func(c *agentlink.MCPExecutionContext) {
		c.Binding.Task.AgentID = "other-task"
		c.Binding.Actor = c.Binding.Task
	}, func(c *agentlink.MCPExecutionContext) { c.Binding.RunID = "old-run" }, func(c *agentlink.MCPExecutionContext) { c.Claim.ClaimID = "forged-claim" }, func(c *agentlink.MCPExecutionContext) { c.Claim.ConnectionID = "other-connection" }} {
		forged := input
		mutate(&forged.Context)
		result, e := g.receiver.CallTool(ctx, "pot_receiver_task_submit", json.RawMessage(mcpJSON(forged)))
		if e == nil && !result.IsError {
			t.Fatal("forged execution accepted", forged)
		}
	}
	// Ordinary OAuth never gets an execution tool or becomes the Task actor.
	result, e := g.caller.CallTool(ctx, "pot_receiver_task_submit", json.RawMessage(mcpJSON(input)))
	if e == nil && !result.IsError {
		t.Fatal("ordinary OAuth became execution")
	}
	var receipt agentlink.MCPEventReceipt
	fixtureCall(t, g.receiver, "pot_receiver_event", agentlink.MCPExecutionEvent{Claim: g.claim, Binding: g.binding, Sequence: 2, Phase: protocol.TaskComplete, Result: &protocol.TaskResultEvent{TaskID: g.binding.Task.AgentID, RunID: g.binding.RunID, Title: "done", Phase: protocol.TaskComplete, Origin: protocol.TaskOrigin{Kind: "manual"}}}, &receipt)
	fixtureCallError(t, g.receiver, "pot_receiver_task_submit", input, agentlink.MCPFenced)
}
func TestMCPHumanApprovalHTTPAndOwnerPortAreOneShotAndFenced(t *testing.T) {
	g := newGatewayFixture(t)
	ctx := context.Background()
	pending := agentlink.MCPApprovalPending{Context: g.execution, Epoch: "native-epoch", RequestID: "origin", Approval: agentlink.ExecutionApproval{ID: "approval", JobID: g.binding.Task.AgentID, Method: "command", Params: json.RawMessage(`{"command":"fixture only"}`)}}
	var saved agentlink.MCPApprovalPending
	fixtureCall(t, g.receiver, "pot_receiver_approval_pending", pending, &saved)
	fixtureCall(t, g.receiver, "pot_receiver_approval_pending", pending, &saved)
	s := g.f.access.options.Local
	reviews := g.f.access.humanReviews(ctx, "main")
	if len(reviews) != 1 || reviews[0].Caller != g.origin {
		t.Fatal("original user lost", reviews)
	}
	badUser := g.origin
	badUser.AgentID = "different-user"
	if e := g.f.access.decideHumanApproval(ctx, reviews[0], badUser, "allow_once"); !errors.Is(e, agentlink.ErrUnauthorized) {
		t.Fatal("cross user approval", e)
	}
	stale := reviews[0]
	stale.Pending.Context.Binding.RunID = "old-run"
	if e := g.f.access.decideHumanApproval(ctx, stale, g.origin, "allow_once"); !errors.Is(e, agentlink.ErrFenced) {
		t.Fatal("stale run approval", e)
	}
	// Model MCP catalog has no human decision method even with receiver grant.
	result, e := g.receiver.CallTool(ctx, "pot_receiver_approval_decide", json.RawMessage(`{}`))
	if e == nil && !result.IsError {
		t.Fatal("model approval exposed")
	}
	view := s.reviewView(ctx, "main", nil, nil)
	if len(view["pending"].([]any)) != 1 {
		t.Fatal("owner review UI not wired", view)
	}
	if e = s.decideLinkedApproval(ctx, "main", mcpHumanApprovalID(reviews[0]), "allow_once"); e != nil {
		t.Fatal(e)
	}
	if e = s.decideLinkedApproval(ctx, "main", mcpHumanApprovalID(reviews[0]), "allow_once"); !errors.Is(e, agentlink.ErrNotFound) {
		t.Fatal("duplicate decision", e)
	}
	var delivery MCPClaimedDelivery
	fixtureCall(t, g.receiver, "pot_receiver_claim", map[string]any{"request_id": "decision", "wait_ms": 0}, &delivery)
	if delivery.Control == nil || delivery.Control.Approval == nil || delivery.Control.Caller != g.origin || delivery.Control.Approval.User != g.origin || delivery.Control.Binding != g.binding || delivery.Control.Approval.RequestID != "origin" {
		t.Fatal("decision mapping lost", delivery)
	}
	var receipt agentlink.MCPEventReceipt
	fixtureCall(t, g.receiver, "pot_receiver_event", agentlink.MCPExecutionEvent{Claim: g.claim, Binding: g.binding, Sequence: 2, Phase: protocol.TaskComplete, Result: &protocol.TaskResultEvent{TaskID: g.binding.Task.AgentID, RunID: g.binding.RunID, Title: "done", Phase: protocol.TaskComplete, Origin: protocol.TaskOrigin{Kind: "manual"}}}, &receipt)
	if e = g.f.access.decideHumanApproval(ctx, reviews[0], g.origin, "deny"); !errors.Is(e, agentlink.ErrFenced) {
		t.Fatal("terminal decision accepted", e)
	}
}

func TestMCPNodeExecutionAttestationPreservesWorkerRestrictions(t *testing.T) {
	g := newGatewayFixture(t)
	ctx := context.Background()
	binding := agentlink.MCPExecutionBinding{Actor: protocol.AgentRef{NodeID: g.identity.NodeID(), Kind: "task", AgentID: "locally-created-task"}, Task: protocol.AgentRef{NodeID: g.identity.NodeID(), Kind: "task", AgentID: "locally-created-task"}, RunID: "local-run"}
	admission := agentlink.MCPNodeExecutionAdmission{ExecutionID: "local-active", Binding: binding}
	var saved agentlink.MCPNodeExecutionAdmission
	fixtureCall(t, g.receiver, "pot_receiver_execution_admit", admission, &saved)
	fixtureCall(t, g.receiver, "pot_receiver_execution_admit", admission, &saved)
	trusted := agentlink.MCPExecutionContext{NodeExecutionID: admission.ExecutionID, Binding: binding}
	if c, _, e := g.f.access.executionCaller(ctx, g.identity, trusted); e != nil || c.Actor() != binding.Task || c.Execution().RunID != binding.RunID {
		t.Fatal("root task identity lost", c, e)
	}
	target := agentlink.AssistantTarget(g.f.queue.NodeID, "main")
	fixtureCallError(t, g.receiver, "pot_receiver_task_submit", agentlink.MCPExecutionSubmit{Context: trusted, Input: agentlink.MCPSubmitInput{RequestID: "worker-must-not-delegate", Target: target, Text: "forbidden"}}, agentlink.MCPUnauthorized)
	fixtureCallError(t, g.receiver, "pot_receiver_message", agentlink.MCPExecutionMessage{Context: trusted, Input: agentlink.MCPMessageInput{RequestID: "worker-must-not-message-remote", Message: protocol.SendMail{To: target, Text: "forbidden"}}}, agentlink.MCPUnauthorized)
	forged := admission
	forged.ExecutionID = "foreign"
	forged.Binding.Task.NodeID = "foreign-node"
	forged.Binding.Actor = forged.Binding.Task
	fixtureCallError(t, g.receiver, "pot_receiver_execution_admit", forged, agentlink.MCPUnauthorized)
	next := admission
	next.ExecutionID = "next-local"
	next.Binding.RunID = "next-run"
	next.PreviousRunID = binding.RunID
	fixtureCallError(t, g.receiver, "pot_receiver_execution_admit", next, agentlink.MCPFenced)
	var end agentlink.MCPNodeExecutionEnd
	fixtureCall(t, g.receiver, "pot_receiver_execution_end", agentlink.MCPNodeExecutionEnd{ExecutionID: admission.ExecutionID, Binding: binding}, &end)
	if _, _, e := g.f.access.executionCaller(ctx, g.identity, trusted); !errors.Is(e, agentlink.ErrFenced) {
		t.Fatal("ended local execution remained live", e)
	}
	fixtureCallError(t, g.receiver, "pot_receiver_execution_admit", admission, agentlink.MCPFenced)
	fixtureCall(t, g.receiver, "pot_receiver_execution_admit", next, &saved)
	if _, _, e := g.f.access.executionCaller(ctx, g.identity, trusted); !errors.Is(e, agentlink.ErrFenced) {
		t.Fatal("old root run crossed CAS", e)
	}
	// Direct inbound Task actors have exactly the same capability restrictions.
	fixtureCallError(t, g.receiver, "pot_receiver_task_submit", agentlink.MCPExecutionSubmit{Context: g.execution, Input: agentlink.MCPSubmitInput{RequestID: "inbound-worker", Target: target, Text: "forbidden"}}, agentlink.MCPUnauthorized)
}
func TestMCPHumanPendingInvalidatedByNativeEpochChange(t *testing.T) {
	g := newGatewayFixture(t)
	pending := agentlink.MCPApprovalPending{Context: g.execution, Epoch: "native-epoch", RequestID: "origin", Approval: agentlink.ExecutionApproval{ID: "restart-review", JobID: g.binding.Task.AgentID, Method: "command", Params: json.RawMessage(`{}`)}}
	var saved agentlink.MCPApprovalPending
	fixtureCall(t, g.receiver, "pot_receiver_approval_pending", pending, &saved)
	before := g.f.access.humanReviews(context.Background(), "main")
	if len(before) != 1 {
		t.Fatal(before)
	}
	registration := fixtureRegistration()
	registration.Capabilities.Operations = append(registration.Capabilities.Operations, agentlink.Approvals)
	registration.Epoch = "restarted-native-epoch"
	var result any
	fixtureCall(t, g.receiver, "pot_receiver_register", registration, &result)
	if len(g.f.access.humanReviews(context.Background(), "main")) != 0 {
		t.Fatal("in-memory pending falsely restored")
	}
	if e := g.f.access.decideHumanApproval(context.Background(), before[0], g.origin, "allow_once"); !errors.Is(e, agentlink.ErrFenced) {
		t.Fatal("restart decision accepted", e)
	}
}

func TestMCPReceiverOfflineAdmissionDoesNotEraseAcceptedRetry(t *testing.T) {
	g := newGatewayFixture(t)
	ctx := context.Background()
	g.f.queue.liveMu.Lock()
	g.f.queue.live[g.identity.ConnectionID()] = time.Now().Add(-2 * mcpReceiverLease)
	g.f.queue.liveMu.Unlock()
	var original MCPTaskAcceptance
	fixtureCall(t, g.caller, "pot_task_submit", agentlink.MCPSubmitInput{RequestID: "origin", Target: g.binding.Receiver, Text: "fixture"}, &original)
	if !original.Receipt.Duplicate {
		t.Fatal("offline erased accepted retry")
	}
	fixtureCallError(t, g.caller, "pot_task_submit", agentlink.MCPSubmitInput{RequestID: "offline-new", Target: g.binding.Receiver, Text: "must not admit"}, agentlink.MCPUnavailable)
	if g.f.queue.ReceiverOnline(g.identity.ConnectionID()) {
		t.Fatal("saved registration asserted liveness")
	}
	_ = ctx
}

func TestMCPNodeMailPreservesOriginalUserReplyAndDedup(t *testing.T) {
	g := gatewayFixtureFor(t, true)
	ctx := context.Background()
	var parentRaw string
	if e := g.f.queue.DB.QueryRow("SELECT envelope FROM agent_messages WHERE direction='out' LIMIT 1").Scan(&parentRaw); e != nil {
		t.Fatal(e)
	}
	var parent protocol.Envelope
	if json.Unmarshal([]byte(parentRaw), &parent) != nil {
		t.Fatal(parentRaw)
	}
	reply := protocol.Envelope{ID: "node-reply", Kind: "message", From: parent.To, To: parent.From, ConversationID: parent.ConversationID, ReplyTo: &parent.ID, Hops: parent.Hops + 1, Text: "node reply"}
	input := agentlink.MCPNodeMail{Message: reply, ProcessingParentID: parent.ID}
	var accepted MCPAccessMessageAcceptance
	fixtureCall(t, g.receiver, "pot_receiver_node_mail", input, &accepted)
	if mcpJSON(accepted.Message.Message) != mcpJSON(reply) {
		t.Fatal("immutable reply changed", accepted)
	}
	var state string
	if e := g.f.queue.DB.QueryRow("SELECT state FROM agent_messages WHERE id=?", reply.ID).Scan(&state); e != nil || state != "processed" {
		t.Fatal("user reply became autonomous input", state, e)
	}
	fixtureCall(t, g.receiver, "pot_receiver_node_mail", input, &accepted)
	if !accepted.Receipt.Duplicate {
		t.Fatal("node mail replayed")
	}
	input.Message.Text = "conflicting bytes"
	fixtureCallError(t, g.receiver, "pot_receiver_node_mail", input, agentlink.MCPConflict)
	_ = ctx
}
