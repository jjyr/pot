package control

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

type linkedReview struct {
	LinkID string
	Name   string
	Caller protocol.AgentRef
	agentlink.ReviewState
}

// The owner UI reviews only this assistant's directly delegated tasks.
// Models have no access to this port and cannot select an approval identity.
func (s *Service) linkedReviews(ctx context.Context, owner string) []linkedReview {
	result := []linkedReview{}
	if s.agentLinks == nil || s.agentLinks.service == nil {
		return result
	}
	connections, err := s.agentLinks.service.List(ctx)
	if err != nil {
		return result
	}
	target, err := AgentTarget(owner)
	if err != nil || target.LinkID != "" {
		return result
	}
	for _, c := range connections {
		if c.State == "revoked" || c.Directory == nil || c.Directory.TaskExecutor == nil || !c.Directory.TaskExecutor.Supports(agentlink.Approvals) {
			continue
		}
		var delegated bool
		_ = s.integrationDB.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM agent_link_delegations WHERE owner=? AND link_id=?)", owner, c.ID).Scan(&delegated)
		if !delegated {
			continue
		}
		result = append(result, linkedReview{LinkID: c.ID, Name: c.Peer.Name, Caller: protocol.AgentRef{NodeID: s.options.RuntimeID, Kind: "assistant", AgentID: owner}})
		if len(result) == 16 {
			break
		}
	}
	var wg sync.WaitGroup
	for i := range result {
		wg.Add(1)
		go func(item *linkedReview) {
			defer wg.Done()
			readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			state, err := s.agentLinks.service.Review(readCtx, item.LinkID, agentlink.ReviewRequest{From: s.reviewUser(item.LinkID), Caller: item.Caller, Action: "read"})
			if err != nil {
				return
			}
			item.ReviewState = state
			if state.Host != "" {
				item.Name += " · " + state.Host
			}
		}(&result[i])
	}
	wg.Wait()
	return result
}

func (s *Service) reviewUser(link string) protocol.AgentRef {
	return protocol.AgentRef{NodeID: s.options.RuntimeID, Kind: "user", AgentID: linkChatID(link, "operator")}
}

func linkedApprovalID(item linkedReview, id string) string {
	hash, _ := core.HashJSON([]string{item.LinkID, id})
	return "linked:" + hash
}

func (s *Service) decideLinkedApproval(ctx context.Context, owner, id, action string) error {
	if action != "allow_once" && action != "deny" {
		return agentlink.ErrInvalid
	}
	for _, item := range s.linkedReviews(ctx, owner) {
		for _, p := range item.Pending {
			if linkedApprovalID(item, p.ID) != id {
				continue
			}
			_, err := s.agentLinks.service.Review(ctx, item.LinkID, agentlink.ReviewRequest{From: s.reviewUser(item.LinkID), Caller: item.Caller, ID: p.ID, Action: action})
			return err
		}
	}
	return agentlink.ErrNotFound
}

func (s *Service) reviewView(ctx context.Context, owner string, policy any, pending []any) map[string]any {
	linked := s.linkedReviews(ctx, owner)
	for _, item := range linked {
		for _, p := range item.Pending {
			var task string
			_ = s.integrationDB.QueryRowContext(ctx, "SELECT id FROM agent_link_delegations WHERE owner=? AND link_id=? AND remote_id=?", owner, item.LinkID, p.JobID).Scan(&task)
			if task == "" {
				continue
			}
			var args map[string]any
			_ = json.Unmarshal(p.Params, &args)
			if args == nil {
				args = map[string]any{}
			}
			args["cmd"] = args["command"]
			args["justification"] = args["reason"]
			pending = append(pending, map[string]any{"id": linkedApprovalID(item, p.ID), "once_only": true, "source": item.Name, "details": p.Params, "request": map[string]any{"owner": owner, "task": task, "tool": p.Method, "args": args, "call_id": p.ID, "turn": p.JobID, "machine_id": item.Name}})
		}
	}
	return map[string]any{"policy": policy, "pending": pending}
}

func isLinkedApproval(id string) bool { return strings.HasPrefix(id, "linked:") }
