package control

import (
	"context"
	"encoding/json"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/protocol"
	"sync"
	"testing"
)

// This adapter fixture checks SDK role/routing and exact forwarding. Real two
// Control PermissionStore/ManageApproval behavior is owned by the Pot receiver.
type reverseHumanFixture struct {
	mu         sync.Mutex
	review     agentlink.MCPHumanReview
	connection string
	decided    bool
}

func (b *reverseHumanFixture) MCPHumanReviews(_ context.Context, r agentlink.MCPReceiver, c agentlink.MCPCaller, in agentlink.MCPHumanReviewRead) (agentlink.MCPHumanReviewState, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if r.ConnectionID() != b.connection || c.Actor() != in.Caller {
		return agentlink.MCPHumanReviewState{}, agentlink.ErrUnauthorized
	}
	if b.review.Caller == (protocol.AgentRef{}) {
		b.review.Caller = c.Actor()
	}
	if b.review.Caller != c.Actor() {
		return agentlink.MCPHumanReviewState{}, agentlink.ErrUnauthorized
	}
	result := agentlink.MCPHumanReviewState{Pending: []agentlink.MCPHumanReview{}}
	if !b.decided {
		result.Pending = append(result.Pending, b.review)
	}
	return result, nil
}
func (b *reverseHumanFixture) MCPHumanDecision(_ context.Context, r agentlink.MCPReceiver, c agentlink.MCPCaller, in agentlink.MCPHumanReviewDecision) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	p := b.review
	if r.ConnectionID() != b.connection || c.Actor() != p.Caller || in.Caller != p.Caller {
		return agentlink.ErrUnauthorized
	}
	if b.decided || in.RequestID != p.RequestID || in.TaskID != p.TaskID || in.ExecutionTask != p.ExecutionTask || in.RunID != p.RunID || in.Generation != p.Generation || in.Epoch != p.Epoch || in.ApprovalID != p.Approval.ID {
		return agentlink.ErrFenced
	}
	b.decided = true
	return nil
}
func TestMCPReverseHumanReviewIsConditionalAndReceiverOnly(t *testing.T) {
	ctx := context.Background()
	// The bare queue has no human backend. A real Service now implements the
	// conditional interface and is covered by the two-Control tests.
	bare := mcpAccessTestFixture(t)
	receiver := bare.session(t, bare.grant(t, true))
	tools, e := receiver.ListTools(ctx)
	if e != nil {
		t.Fatal(e)
	}
	for _, tool := range tools {
		if tool.Name == "pot_receiver_human_review_decide" {
			t.Fatal("unimplemented backend advertised")
		}
	}
	g := gatewayFixtureFor(t, true)
	backend := &reverseHumanFixture{connection: g.identity.ConnectionID(), review: agentlink.MCPHumanReview{Caller: g.execution.Binding.Actor, RequestID: "original-delegation", TaskID: "accepted-public-task", ExecutionTask: protocol.AgentRef{NodeID: g.f.queue.NodeID, Kind: "task", AgentID: "actual-local-task"}, RunID: "actual-local-run", Generation: 1, Epoch: "actual-server-epoch", Approval: agentlink.ExecutionApproval{ID: "actual-pending", JobID: "actual-local-task", Method: "exec_command", Params: json.RawMessage(`{"cmd":"fixture"}`)}}}
	g.f.access.options.HumanReview = backend
	g.f.access.reverseHumanReviewTools()
	var state agentlink.MCPHumanReviewState
	fixtureCall(t, g.receiver, "pot_receiver_human_review_read", agentlink.MCPHumanReviewRead{Caller: backend.review.Caller}, &state)
	if len(state.Pending) != 1 || state.Pending[0].Caller != backend.review.Caller || state.Pending[0].ExecutionTask != backend.review.ExecutionTask {
		t.Fatal("review provenance changed", state)
	}
	p := state.Pending[0]
	decision := agentlink.MCPHumanReviewDecision{Caller: p.Caller, RequestID: p.RequestID, TaskID: p.TaskID, ExecutionTask: p.ExecutionTask, RunID: p.RunID, Generation: p.Generation, Epoch: p.Epoch, ApprovalID: p.Approval.ID, User: protocol.AgentRef{NodeID: g.identity.NodeID(), Kind: "user", AgentID: linkChatID(g.identity.ConnectionID(), "operator")}, Action: "allow_once"}
	wrong := decision
	wrong.Caller = g.binding.Task
	fixtureCallError(t, g.receiver, "pot_receiver_human_review_decide", wrong, agentlink.MCPUnauthorized)
	wrong = decision
	wrong.RunID = "stale-run"
	fixtureCallError(t, g.receiver, "pot_receiver_human_review_decide", wrong, agentlink.MCPFenced)
	wrong = decision
	wrong.User.NodeID = "other-node"
	fixtureCallError(t, g.receiver, "pot_receiver_human_review_decide", wrong, agentlink.MCPUnauthorized)
	result, e := g.caller.CallTool(ctx, "pot_receiver_human_review_decide", json.RawMessage(mcpJSON(decision)))
	if e == nil && !result.IsError {
		t.Fatal("ordinary model caller reached human decision")
	}
	var accepted map[string]bool
	fixtureCall(t, g.receiver, "pot_receiver_human_review_decide", decision, &accepted)
	if !accepted["accepted"] {
		t.Fatal(accepted)
	}
	fixtureCallError(t, g.receiver, "pot_receiver_human_review_decide", decision, agentlink.MCPFenced)
}
func TestMCPReverseHumanReadDerivesAuthenticatedOrdinaryUser(t *testing.T) {
	g := newGatewayFixture(t)
	backend := &reverseHumanFixture{connection: g.identity.ConnectionID(), review: agentlink.MCPHumanReview{RequestID: "user-delegation", TaskID: "accepted-public-task", ExecutionTask: protocol.AgentRef{NodeID: g.f.queue.NodeID, Kind: "task", AgentID: "actual-local"}, RunID: "run", Generation: 1, Epoch: "boot", Approval: agentlink.ExecutionApproval{ID: "pending", JobID: "actual-local", Method: "fixture", Params: json.RawMessage(`{}`)}}}
	g.f.access.options.HumanReview = backend
	g.f.access.reverseHumanReviewTools()
	var state agentlink.MCPHumanReviewState
	fixtureCall(t, g.receiver, "pot_receiver_human_review_read", agentlink.MCPHumanReviewRead{}, &state)
	if len(state.Pending) != 1 || state.Pending[0].Caller.Kind != "user" || state.Pending[0].Caller.NodeID != g.f.queue.NodeID {
		t.Fatal("ordinary user became receiver node", state)
	}
	forged := state.Pending[0].Caller
	forged.AgentID = "other-user"
	fixtureCallError(t, g.receiver, "pot_receiver_human_review_read", agentlink.MCPHumanReviewRead{Caller: forged}, agentlink.MCPUnauthorized)
}
