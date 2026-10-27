package control

import (
	"context"
	"strings"
	"time"

	"github.com/jjyr/pot/internal/mcpreceiver"
	"github.com/jjyr/pot/internal/tasks"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

func (s *Service) isPotMCP(ctx context.Context, id string) bool {
	var found int
	if s.integrationDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM mcp_receiver_connections WHERE id=? AND node_id=?", id, s.options.RuntimeID).Scan(&found) == nil && found == 1 {
		return true
	}
	_, e := s.inboundPotMCPConnection(ctx, id)
	return e == nil
}

// potMCPRoute includes saved offline targets so their operations fail explicitly
// through MCP, without falling back to an old pairing for the same node.
func (s *Service) potMCPRoute(ctx context.Context, target protocol.AgentRef) (PotMCPConnection, bool, error) {
	connections, e := s.PotMCPConnections(ctx)
	if e != nil {
		return PotMCPConnection{}, false, e
	}
	incoming, e := s.inboundPotMCPConnections(ctx)
	if e != nil {
		return PotMCPConnection{}, false, e
	}
	connections = append(connections, incoming...)
	var selected PotMCPConnection
	found := false
	for _, c := range connections {
		if c.Directory == nil {
			continue
		}
		for _, endpoint := range c.Directory.Targets {
			if endpoint.Address != target {
				continue
			}
			if found {
				return selected, true, agentlink.ErrConflict
			}
			selected = c
			found = true
		}
	}
	return selected, found, nil
}
func (s *Service) potMCPCall(ctx context.Context, id, tool string, input, output any) error {
	c, e := s.PotMCPConnection(ctx, id)
	if e != nil {
		return e
	}
	if c.State != "active" {
		return agentlink.ErrDisconnected
	}
	s.potMCP.mu.Lock()
	live := s.potMCP.live[id]
	var client = (*mcpreceiver.Client)(nil)
	if live != nil && live.online {
		client = live.client
	}
	s.potMCP.mu.Unlock()
	if client == nil {
		return agentlink.ErrUnavailable
	}
	return client.Call(ctx, tool, input, output)
}
func (s *Service) createPotMCPTask(ctx context.Context, c PotMCPConnection, owner string, from protocol.AgentRef, request protocol.CreateTask) (protocol.TaskInfo, error) {
	if from != agentlink.AssistantTarget(s.options.RuntimeID, owner) && from != (protocol.AgentRef{NodeID: s.options.RuntimeID, Kind: "user", AgentID: owner}) {
		return protocol.TaskInfo{}, agentlink.ErrUnauthorized
	}
	if request.Target == nil || c.Directory == nil || !c.Directory.Capabilities(*request.Target).Supports(agentlink.TaskSubmit) {
		return protocol.TaskInfo{}, agentlink.ErrUnsupported
	}
	if request.Workspace != (protocol.TaskWorkspace{}) {
		return protocol.TaskInfo{}, agentlink.ErrUnsupported
	}
	if c.Inbound {
		a, caller, e := s.localPotMCPBusinessCaller(ctx, c, from)
		if e != nil {
			return protocol.TaskInfo{}, e
		}
		accepted, e := a.options.Queue.Submit(ctx, caller, agentlink.MCPSubmitInput{RequestID: request.RequestID, Target: *request.Target, Text: request.Text})
		if e != nil {
			return protocol.TaskInfo{}, e
		}
		record, e := s.Tasks.Get(ctx, owner, accepted.Task.ID)
		if e != nil {
			return protocol.TaskInfo{}, e
		}
		if record == nil {
			return protocol.TaskInfo{}, agentlink.ErrNotFound
		}
		return record.Info, nil
	}
	id, _ := core.HashJSON([]string{"agent-link-task", owner, request.RequestID})
	now := time.Now().UnixMilli()
	origin := &protocol.AgentLinkOrigin{LinkID: c.ID, NodeID: request.Target.NodeID, AssistantID: request.Target.AgentID, Kind: "pot", Name: c.Directory.Name}
	r := agentlink.TaskRequest{Target: *request.Target, RequestID: request.RequestID, From: from, Text: request.Text}
	record := tasks.Record{AssistantID: owner, Request: request, Info: protocol.TaskInfo{ID: id, AssistantID: owner, Title: strings.SplitN(request.Text, "\n", 2)[0], Phase: protocol.TaskQueued, RemoteWaiting: true, CreatedAtMS: now, UpdatedAtMS: now, AgentLink: origin, ExecutorIcon: "pot"}, Execution: &tasks.Execution{Request: r}}
	if e := s.Tasks.AcceptExecution(ctx, record); e != nil {
		return protocol.TaskInfo{}, e
	}
	s.wakeTasks()
	s.Changes.Notify("tasks")
	return record.Info, nil
}
func (s *Service) potMCPTaskSubmit(ctx context.Context, id string, r agentlink.TaskRequest) (agentlink.Task, error) {
	var accepted MCPTaskAcceptance
	if r.From.NodeID != s.options.RuntimeID || r.From.Kind != "assistant" && r.From.Kind != "user" {
		return agentlink.Task{}, agentlink.ErrUnauthorized
	}
	if r.From.Kind == "user" {
		e := s.potMCPCall(ctx, id, "pot_task_submit", agentlink.MCPSubmitInput{RequestID: r.RequestID, Target: r.Target, Text: r.Text}, &accepted)
		return accepted.Task, e
	}
	e := s.potMCPCall(ctx, id, "pot_receiver_node_submit", agentlink.MCPNodeSubmit{Actor: r.From, Input: agentlink.MCPSubmitInput{RequestID: r.RequestID, Target: r.Target, Text: r.Text}}, &accepted)
	return accepted.Task, e
}
func (s *Service) potMCPTaskRead(ctx context.Context, id string, r agentlink.TaskRequest, task string) (agentlink.Task, error) {
	var remote agentlink.Task
	if c, e := s.PotMCPConnection(ctx, id); e == nil && c.Inbound {
		a, caller, e := s.localPotMCPBusinessCaller(ctx, c, r.From)
		if e != nil {
			return remote, e
		}
		return a.options.Queue.Task(ctx, caller, task)
	}
	if r.From.Kind == "user" {
		e := s.potMCPCall(ctx, id, "pot_task_read", map[string]string{"task_id": task}, &remote)
		return remote, e
	}
	e := s.potMCPCall(ctx, id, "pot_receiver_node_task_get", agentlink.MCPNodeRead{Actor: r.From, TaskID: task}, &remote)
	return remote, e
}
func (s *Service) potMCPTaskCancel(ctx context.Context, id string, r agentlink.TaskRequest, task, run string) (agentlink.Task, error) {
	if task == "" || run == "" {
		return agentlink.Task{}, agentlink.ErrUnavailable
	}
	if c, e := s.PotMCPConnection(ctx, id); e == nil && c.Inbound {
		a, caller, e := s.localPotMCPBusinessCaller(ctx, c, r.From)
		if e != nil {
			return agentlink.Task{}, e
		}
		request, _ := core.HashJSON([]string{"pot-mcp-cancel", id, task, run})
		accepted, e := a.options.Queue.Control(ctx, caller, nil, &agentlink.MCPCancelInput{RequestID: request, TaskID: task, RunID: run})
		return accepted.Task, e
	}
	request, _ := core.HashJSON([]string{"pot-mcp-cancel", id, task, run})
	var accepted MCPTaskAcceptance
	if r.From.Kind == "user" {
		e := s.potMCPCall(ctx, id, "pot_task_cancel", agentlink.MCPCancelInput{RequestID: request, TaskID: task, RunID: run}, &accepted)
		return accepted.Task, e
	}
	e := s.potMCPCall(ctx, id, "pot_receiver_node_task_cancel", agentlink.MCPNodeCancel{Actor: r.From, Input: agentlink.MCPCancelInput{RequestID: request, TaskID: task, RunID: run}}, &accepted)
	return accepted.Task, e
}
func (s *Service) potMCPTaskInput(ctx context.Context, d *tasks.Record, input protocol.TaskInput) (agentlink.Task, error) {
	var accepted MCPTaskAcceptance
	if c, e := s.PotMCPConnection(ctx, d.Info.AgentLink.LinkID); e == nil && c.Inbound {
		a, caller, e := s.localPotMCPBusinessCaller(ctx, c, d.Execution.Request.From)
		if e != nil {
			return agentlink.Task{}, e
		}
		accepted, e := a.options.Queue.Control(ctx, caller, &agentlink.MCPTaskInput{RequestID: input.RequestID, TaskID: d.Execution.Receipt.ID, RunID: d.Execution.Receipt.RunID, Text: input.Text}, nil)
		return accepted.Task, e
	}
	if d.Execution.Request.From.Kind == "user" {
		e := s.potMCPCall(ctx, d.Info.AgentLink.LinkID, "pot_task_input", agentlink.MCPTaskInput{TaskID: d.Execution.Receipt.ID, RunID: d.Execution.Receipt.RunID, RequestID: input.RequestID, Text: input.Text}, &accepted)
		return accepted.Task, e
	}
	e := s.potMCPCall(ctx, d.Info.AgentLink.LinkID, "pot_receiver_node_task_input", agentlink.MCPNodeInput{Actor: d.Execution.Request.From, Input: agentlink.MCPTaskInput{TaskID: d.Execution.Receipt.ID, RunID: d.Execution.Receipt.RunID, RequestID: input.RequestID, Text: input.Text}}, &accepted)
	return accepted.Task, e
}
func projectPotMCPTaskReceipt(record *tasks.Record, remote agentlink.Task) {
	projectTaskReceipt(record, remote)
	// The backend's actual run is not a UI-generated replacement identity.
	record.Info.RunID = remote.RunID
}

// localPotMCPExecution obtains actor/run from the owning Control's trusted call
// binding and persisted active record. Model parameters cannot provide them.
func (s *Service) localPotMCPExecution(ctx context.Context, owner string, actor protocol.AgentRef) (agentlink.MCPNodeExecutionAdmission, error) {
	var result agentlink.MCPNodeExecutionAdmission
	if actor.NodeID != s.options.RuntimeID || actor.Kind != "task" {
		return result, agentlink.ErrUnauthorized
	}
	bound, ok := ctx.Value(taskMessageRunKey{}).(taskMessageRun)
	if !ok {
		return result, agentlink.ErrUnauthorized
	}
	record, e := s.Tasks.Get(ctx, owner, actor.AgentID)
	if e != nil {
		return result, e
	}
	if record == nil || record.Execution != nil || record.NodeID != s.options.RuntimeID || record.RunID != bound.ID || record.Generation != bound.Generation || record.Info.Phase.Scope() != "active" || record.StopMode != "" {
		return result, agentlink.ErrFenced
	}
	id, _ := core.HashJSON([]string{actor.NodeID, actor.AgentID, record.RunID})
	result = agentlink.MCPNodeExecutionAdmission{ExecutionID: id, Binding: agentlink.MCPExecutionBinding{Actor: actor, Task: actor, RunID: record.RunID}, PreviousRunID: record.PreviousRunID}
	return result, nil
}
