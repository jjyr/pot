package control

import (
	"context"
	"encoding/json"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/mcp"
	"github.com/jjyr/pot/pkg/protocol"
)

// The catalogue includes these methods only when the real owning backend has
// implemented both directions. The local model execution server never includes
// them. Receiver authentication still grants no general owner operation.
func (a *MCPAccess) reverseHumanReviewTools() {
	backend := a.options.HumanReview
	if backend == nil {
		return
	}
	a.tool("pot_receiver_human_review_read", mcpContractSchema(agentlink.MCPHumanReviewRead{}), func(ctx context.Context, r *mcp.CallToolRequest, raw json.RawMessage) (any, error) {
		var in agentlink.MCPHumanReviewRead
		if agentlink.DecodeMCPContract(raw, &in) != nil {
			return nil, agentlink.ErrInvalid
		}
		caller, e := a.humanReviewCaller(ctx, r, in.Caller)
		if e != nil {
			return nil, e
		}
		in.Caller = caller.Actor()
		receiver, e := mcpReceiver(r)
		if e != nil {
			return nil, e
		}
		return backend.MCPHumanReviews(ctx, receiver, caller, in)
	})
	a.tool("pot_receiver_human_review_decide", mcpContractSchema(agentlink.MCPHumanReviewDecision{}), func(ctx context.Context, r *mcp.CallToolRequest, raw json.RawMessage) (any, error) {
		var in agentlink.MCPHumanReviewDecision
		if agentlink.DecodeMCPContract(raw, &in) != nil {
			return nil, agentlink.ErrInvalid
		}
		caller, e := a.humanReviewCaller(ctx, r, in.Caller)
		if e != nil {
			return nil, e
		}
		if in.Caller != caller.Actor() {
			return nil, agentlink.ErrUnauthorized
		}
		receiver, e := mcpReceiver(r)
		if e != nil {
			return nil, e
		}
		if !core.Identifier(in.RequestID) || !core.Identifier(in.TaskID) || !in.ExecutionTask.Valid() || in.ExecutionTask.Kind != "task" || in.ExecutionTask.NodeID != a.options.Queue.NodeID || !core.Identifier(in.RunID) || in.Generation == 0 || in.Generation > core.MaxSafeInteger || !core.Identifier(in.ApprovalID) || !core.Identifier(in.Epoch) || !in.User.Valid() || in.User.Kind != "user" || in.User.NodeID != receiver.NodeID() || (in.Action != "allow_once" && in.Action != "deny") {
			return nil, agentlink.ErrUnauthorized
		}
		return map[string]bool{"accepted": true}, backend.MCPHumanDecision(ctx, receiver, caller, in)
	})
}

func (a *MCPAccess) humanReviewCaller(ctx context.Context, r *mcp.CallToolRequest, actor protocol.AgentRef) (agentlink.MCPCaller, error) {
	if _, e := mcpReceiver(r); e != nil {
		return agentlink.MCPCaller{}, e
	}
	if actor == (protocol.AgentRef{}) {
		return mcpCaller(r)
	}
	if actor.Kind == "user" {
		caller, e := mcpCaller(r)
		if e != nil || caller.Actor() != actor {
			return agentlink.MCPCaller{}, agentlink.ErrUnauthorized
		}
		return caller, nil
	}
	return a.registeredAssistant(ctx, r, actor)
}
