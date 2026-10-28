package control

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jjyr/pot/internal/hooks"
	"github.com/jjyr/pot/internal/tasks"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

var _ agentlink.MCPHumanReviewBackend = (*Service)(nil)

// localMCPHumanTask checks the original accepted caller under this logical
// connection before exposing any local pending permission request.
func (s *Service) localMCPHumanTask(ctx context.Context, receiver agentlink.MCPReceiver, caller agentlink.MCPCaller, public string) (string, string, string, error) {
	if receiver.ConnectionID() != caller.ConnectionID() {
		return "", "", "", agentlink.ErrUnauthorized
	}
	var owner, task, request string
	e := s.integrationDB.QueryRowContext(ctx, "SELECT assistant_id,task_id,request_id FROM agent_link_received_tasks WHERE id=? AND caller=?", public, callerKey(caller.Actor())).Scan(&owner, &task, &request)
	if e != nil {
		return "", "", "", mcpMissing(e)
	}
	var present int
	e = s.Tasks.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM mcp_local_mutations WHERE connection_id=? AND actor=? AND operation=? AND request_id=? AND state='accepted' AND json_extract(receipt,'$.task.id')=?", receiver.ConnectionID(), mcpJSON(caller.Actor()), agentlink.TaskSubmit, request, public).Scan(&present)
	if e != nil {
		return "", "", "", e
	}
	if present != 1 {
		return "", "", "", agentlink.ErrUnauthorized
	}
	return owner, task, request, nil
}
func (s *Service) MCPHumanReviews(ctx context.Context, receiver agentlink.MCPReceiver, caller agentlink.MCPCaller, input agentlink.MCPHumanReviewRead) (agentlink.MCPHumanReviewState, error) {
	state := agentlink.MCPHumanReviewState{Pending: []agentlink.MCPHumanReview{}}
	if input.Caller != caller.Actor() {
		return state, agentlink.ErrUnauthorized
	}
	rows, e := s.integrationDB.QueryContext(ctx, "SELECT id FROM agent_link_received_tasks WHERE caller=? AND task_id!='' ORDER BY id", callerKey(caller.Actor()))
	if e != nil {
		return state, e
	}
	var ids []string
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return state, e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return state, e
	}
	for _, id := range ids {
		owner, task, request, e := s.localMCPHumanTask(ctx, receiver, caller, id)
		if errors.Is(e, agentlink.ErrUnauthorized) {
			continue
		}
		if e != nil {
			return state, e
		}
		record, e := s.Tasks.Get(ctx, owner, task)
		if e != nil {
			return state, e
		}
		if record == nil || record.Execution != nil || record.NodeID != s.options.RuntimeID || record.Info.Phase.Scope() != "active" || record.StopMode != "" {
			continue
		}
		for _, pending := range s.Permissions.Pending(owner) {
			r := pending.Request
			if r.Task != task || r.Turn != record.RunID || r.Generation != record.Generation {
				continue
			}
			state.Pending = append(state.Pending, agentlink.MCPHumanReview{Caller: caller.Actor(), RequestID: request, TaskID: id, ExecutionTask: protocol.AgentRef{NodeID: s.options.RuntimeID, Kind: "task", AgentID: task}, RunID: record.RunID, Generation: record.Generation, Epoch: s.options.BootID, Approval: agentlink.ExecutionApproval{ID: pending.ID, JobID: task, Method: r.Tool, Params: json.RawMessage(r.Args)}})
			if len(state.Pending) >= 64 {
				return state, nil
			}
		}
	}
	return state, nil
}
func (s *Service) MCPHumanDecision(ctx context.Context, receiver agentlink.MCPReceiver, caller agentlink.MCPCaller, input agentlink.MCPHumanReviewDecision) error {
	if input.Caller != caller.Actor() || input.Epoch != s.options.BootID || (input.Action != "allow_once" && input.Action != "deny") {
		return agentlink.ErrUnauthorized
	}
	owner, task, request, e := s.localMCPHumanTask(ctx, receiver, caller, input.TaskID)
	if e != nil {
		return e
	}
	if input.RequestID != request || input.ExecutionTask != (protocol.AgentRef{NodeID: s.options.RuntimeID, Kind: "task", AgentID: task}) {
		return agentlink.ErrUnauthorized
	}
	human, e := s.originalMCPHuman(ctx, receiver, caller, request)
	if e != nil {
		return e
	}
	if input.User != human {
		return agentlink.ErrUnauthorized
	}
	permissions := s.Permissions
	permissions.mu.Lock()
	defer permissions.mu.Unlock()
	pending := permissions.pending[input.ApprovalID]
	if pending == nil || pending.invalid {
		return agentlink.ErrFenced
	}
	e = storage.Transaction(ctx, s.Tasks.DB(), func(tx *sql.Tx) error {
		record, e := s.Tasks.ReadReceiverTx(ctx, tx, owner, task)
		if e != nil {
			return e
		}
		if record == nil || record.Execution != nil || record.NodeID != s.options.RuntimeID || record.RunID != input.RunID || record.Generation != input.Generation || record.Info.Phase.Scope() != "active" || record.StopMode != "" {
			return agentlink.ErrFenced
		}
		actual := pending.approval.Request
		if actual.Owner != owner || actual.Task != task || actual.Turn != input.RunID || actual.Generation != input.Generation {
			return agentlink.ErrFenced
		}
		if pending.resolved && pending.action != input.Action {
			return agentlink.ErrConflict
		}
		return nil
	})
	if e != nil {
		return e
	}
	if !pending.resolved {
		pending.resolved = true
		pending.allow = input.Action == "allow_once"
		pending.action = input.Action
		close(pending.done)
		permissions.approvalHookLocked(hooks.ApprovalResolved, pending)
	}
	return nil
}

type potMCPOutboundReview struct {
	connection PotMCPConnection
	review     agentlink.MCPHumanReview
	task       *tasks.Record
}

func (s *Service) outboundPotMCPReviews(ctx context.Context, owner string) []potMCPOutboundReview {
	localOwner := owner
	link := ""
	asUser := false
	if strings.HasPrefix(owner, "agent-link:") {
		parts := strings.SplitN(strings.TrimPrefix(owner, "agent-link:"), ":", 2)
		if len(parts) != 2 {
			return nil
		}
		link = parts[0]
		asUser = true
		if e := s.integrationDB.QueryRowContext(ctx, "SELECT id FROM agent_link_chat_sessions WHERE link_id=? AND assistant_id=?", parts[0], parts[1]).Scan(&localOwner); e != nil {
			return nil
		}
	}
	connections, e := s.PotMCPConnections(ctx)
	if e != nil {
		return nil
	}
	result := []potMCPOutboundReview{}
	for _, c := range connections {
		if c.State != "active" || link != "" && c.ID != link {
			continue
		}
		var delegated int
		if e = s.Tasks.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM tasks WHERE assistant_id=? AND json_extract(body,'$.info.agent_link.link_id')=? AND json_extract(body,'$.execution') IS NOT NULL", localOwner, c.ID).Scan(&delegated); e != nil || delegated == 0 {
			continue
		}
		actor := agentlink.AssistantTarget(s.options.RuntimeID, localOwner)
		if asUser {
			actor = protocol.AgentRef{}
		}
		var state agentlink.MCPHumanReviewState
		readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		e = s.potMCPCall(readCtx, c.ID, "pot_receiver_human_review_read", agentlink.MCPHumanReviewRead{Caller: actor}, &state)
		cancel()
		if e != nil {
			continue
		}
		for _, review := range state.Pending {
			var id string
			e = s.Tasks.DB().QueryRowContext(ctx, "SELECT id FROM tasks WHERE assistant_id=? AND json_extract(body,'$.info.agent_link.link_id')=? AND json_extract(body,'$.execution.receipt.id')=? AND json_extract(body,'$.execution.receipt.run_id')=?", localOwner, c.ID, review.TaskID, review.RunID).Scan(&id)
			if e != nil {
				continue
			}
			record, e := s.Tasks.Get(ctx, localOwner, id)
			if e != nil || record == nil || record.Execution == nil {
				continue
			}
			if !asUser && record.Execution.Request.From != review.Caller || asUser && record.Execution.Request.From.Kind != "user" {
				continue
			}
			result = append(result, potMCPOutboundReview{c, review, record})
		}
	}
	return result
}
func potMCPOutboundApprovalID(c PotMCPConnection, review agentlink.MCPHumanReview) string {
	id, _ := core.HashJSON([]any{c.ID, review.Caller, review.RequestID, review.TaskID, review.ExecutionTask, review.RunID, review.Generation, review.Epoch, review.Approval.ID})
	return "linked:mcp-outbound:" + id
}
func (s *Service) MCPRemoteReviewView(ctx context.Context, owner string) []any {
	result := []any{}
	for _, item := range s.outboundPotMCPReviews(ctx, owner) {
		review := item.review
		var args map[string]any
		_ = json.Unmarshal(review.Approval.Params, &args)
		result = append(result, map[string]any{"id": potMCPOutboundApprovalID(item.connection, review), "once_only": true, "source": review.ExecutionTask.NodeID, "details": review.Approval.Params, "request": map[string]any{"owner": owner, "task": item.task.Info.ID, "tool": review.Approval.Method, "args": args, "call_id": review.Approval.ID, "turn": review.RunID, "machine_id": review.ExecutionTask.NodeID}})
	}
	return result
}
func (s *Service) DecideMCPRemoteReview(ctx context.Context, owner, id, action string) error {
	if action != "allow_once" && action != "deny" {
		return agentlink.ErrUnauthorized
	}
	for _, item := range s.outboundPotMCPReviews(ctx, owner) {
		review := item.review
		if potMCPOutboundApprovalID(item.connection, review) != id {
			continue
		}
		user := s.reviewUser(item.connection.ID)
		if item.task.Execution.Request.From.Kind == "user" {
			user = item.task.Execution.Request.From
		}
		decision := agentlink.MCPHumanReviewDecision{Caller: review.Caller, RequestID: review.RequestID, TaskID: review.TaskID, ExecutionTask: review.ExecutionTask, RunID: review.RunID, Generation: review.Generation, Epoch: review.Epoch, ApprovalID: review.Approval.ID, User: user, Action: action}
		var accepted struct {
			Accepted bool `json:"accepted"`
		}
		e := s.potMCPCall(ctx, item.connection.ID, "pot_receiver_human_review_decide", decision, &accepted)
		if e != nil {
			return e
		}
		if !accepted.Accepted {
			return agentlink.ErrFenced
		}
		return nil
	}
	return agentlink.ErrNotFound
}
