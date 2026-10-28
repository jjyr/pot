package control

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/protocol"
)

type mcpRemoteHumanPort interface {
	MCPRemoteReviewView(context.Context, string) []any
	DecideMCPRemoteReview(context.Context, string, string, string) error
}

func (s *Service) reviewUser(link string) protocol.AgentRef {
	return protocol.AgentRef{NodeID: s.options.RuntimeID, Kind: "user", AgentID: linkChatID(link, "operator")}
}

func (s *Service) decideLinkedApproval(ctx context.Context, owner, id, action string) error {
	if action != "allow_once" && action != "deny" {
		return agentlink.ErrInvalid
	}
	if access := s.mcpAccess.Load(); access != nil {
		for _, item := range access.humanReviews(ctx, owner) {
			if mcpHumanApprovalID(item) == id {
				user := s.reviewUser(item.Connection)
				if item.Caller.Kind == "user" {
					user = item.Caller
				}
				return access.decideHumanApproval(ctx, item, user, action)
			}
		}
	}
	if strings.HasPrefix(id, "linked:mcp-outbound:") {
		if remote, ok := any(s).(mcpRemoteHumanPort); ok {
			return remote.DecideMCPRemoteReview(ctx, owner, id, action)
		}
		return agentlink.ErrNotFound
	}
	return agentlink.ErrNotFound
}

func (s *Service) reviewView(ctx context.Context, owner string, policy any, pending []any) map[string]any {
	if access := s.mcpAccess.Load(); access != nil {
		for _, item := range access.humanReviews(ctx, owner) {
			p := item.Pending.Approval
			var args map[string]any
			_ = json.Unmarshal(p.Params, &args)
			if args == nil {
				args = map[string]any{}
			}
			if _, present := args["cmd"]; !present {
				args["cmd"] = args["command"]
			}
			if _, present := args["justification"]; !present {
				args["justification"] = args["reason"]
			}
			pending = append(pending, map[string]any{"id": mcpHumanApprovalID(item), "once_only": true, "source": item.Pending.Context.Binding.Task.NodeID, "details": p.Params, "request": map[string]any{"owner": owner, "task": item.Task, "tool": p.Method, "args": args, "call_id": p.ID, "turn": item.Pending.Context.Binding.RunID, "machine_id": item.Pending.Context.Binding.Task.NodeID}})
		}
	}
	if remote, ok := any(s).(mcpRemoteHumanPort); ok {
		pending = append(pending, remote.MCPRemoteReviewView(ctx, owner)...)
	}
	return map[string]any{"policy": policy, "pending": pending}
}

func isLinkedApproval(id string) bool { return strings.HasPrefix(id, "linked:") }
