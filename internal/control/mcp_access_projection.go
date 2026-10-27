package control

import (
	"context"
	"database/sql"
	"github.com/jjyr/pot/internal/tasks"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/protocol"
)

func (q *MCPAccessQueue) acceptCatalogue(ctx context.Context, tx *sql.Tx, c agentlink.MCPCaller, request agentlink.TaskRequest, task agentlink.Task) error {
	target := request.Target
	address := protocol.AgentRef{NodeID: q.NodeID, Kind: "task", AgentID: task.ID}
	title := []rune(request.Text)
	if len(title) > 120 {
		title = title[:120]
	}
	return q.Tasks.AcceptExecutionTx(ctx, tx, tasks.Record{NodeID: q.NodeID, AssistantID: c.Actor().AgentID, Request: protocol.CreateTask{RequestID: request.RequestID, Text: request.Text, Target: &target}, Info: protocol.TaskInfo{ID: task.ID, AssistantID: c.Actor().AgentID, Address: &address, Title: string(title), Phase: protocol.TaskQueued, CreatedAtMS: task.CreatedAt.UnixMilli(), UpdatedAtMS: task.UpdatedAt.UnixMilli(), AgentLink: &protocol.AgentLinkOrigin{LinkID: c.ConnectionID(), NodeID: target.NodeID, AssistantID: target.AgentID, Kind: target.Kind, Name: task.Receiver.Name}}, Execution: &tasks.Execution{Request: request, Receipt: task}})
}
func (q *MCPAccessQueue) projectCatalogue(ctx context.Context, tx *sql.Tx, task agentlink.Task, binding agentlink.MCPRunBinding, expected string, result *protocol.TaskResultEvent) error {
	var message bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM mcp_service_deliveries WHERE task_id=? AND work!='' AND json_type(work,'$.message')='object')", task.ID).Scan(&message); err != nil {
		return err
	}
	if message {
		return nil
	}
	var actorRaw string
	if err := tx.QueryRowContext(ctx, "SELECT actor FROM mcp_service_tasks WHERE id=?", task.ID).Scan(&actorRaw); err != nil {
		return err
	}
	var actor protocol.AgentRef
	if err := mcpRead(actorRaw, &actor); err != nil {
		return err
	}
	record, err := q.Tasks.GetTx(ctx, tx, actor.AgentID, task.ID)
	if err != nil {
		return err
	}
	if record == nil || record.Execution == nil {
		return agentlink.ErrFenced
	}
	record.Execution.Receipt = task
	record.Execution.Receipt.ID = binding.Task.AgentID
	record.Info.RunID = binding.RunID
	record.Info.Phase = protocol.TaskPhase(task.State)
	record.Info.UpdatedAtMS = task.UpdatedAt.UnixMilli()
	if record.Info.Phase == protocol.TaskRunning && record.Info.StartedAtMS == 0 {
		record.Info.StartedAtMS = task.UpdatedAt.UnixMilli()
	}
	if record.Info.Phase.Scope() == "attention" {
		record.Info.FinishedAtMS = task.UpdatedAt.UnixMilli()
	} else {
		record.Info.FinishedAtMS = 0
	}
	if result != nil {
		copied := *result
		copied.TaskID = record.Info.ID
		copied.RunID = binding.RunID
		copied.Origin = record.Origin
		copied.Title = record.Info.Title
		copied.SuppressionReason = ""
		record.Outcome = &copied
	}
	changed, err := q.Tasks.UpdateExecutionTx(ctx, tx, *record, expected)
	if err == nil && !changed {
		return agentlink.ErrFenced
	}
	return err
}
