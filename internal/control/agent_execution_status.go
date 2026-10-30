package control

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/internal/assistant"
	"github.com/jjyr/pot/internal/tasks"
	"github.com/jjyr/pot/pkg/agent"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
	"sort"
)

// AgentExecutionStatus observes the same conversation and task receipts as the UI. It does
// not run models, submit work, retry tools, or infer success from an idle agent.
func (s *Service) AgentExecutionStatus(ctx context.Context, owner, request string) (protocol.AgentExecutionStatus, error) {
	out := protocol.AgentExecutionStatus{AssistantID: owner, RequestID: request, State: "running", ReplyIDs: []string{}, Tasks: []protocol.AgentExecutionProgress{}, Turns: []string{}, Assistant: protocol.AgentExecutionProgress{ID: owner, State: "running", ActiveTools: []string{}}}
	if !core.Identifier(owner) || !core.Identifier(request) {
		return out, errors.New("invalid execution identity")
	}
	c, err := s.Conversation(ctx, owner)
	if err != nil {
		return out, err
	}
	snap, err := c.Snapshot(ctx)
	if err != nil {
		return out, err
	}
	out.BootID = snap.BootID
	out.ConversationID = snap.ConversationID
	out.ConversationCursor = snap.Cursor
	inputs := map[string]string{}
	since := int64(0)
	for _, m := range snap.Messages {
		if (m.Role == protocol.User || m.Role == protocol.System) && m.RequestID != nil && !m.Steering {
			inputs[*m.RequestID] = m.ID
			if *m.RequestID == request {
				out.MessageID = m.ID
				since = m.CreatedAtMS
			}
		}
	}
	if out.MessageID == "" {
		return out, protocol.APIError{Code: "not_found", Message: "execution request not found"}
	}
	turns := map[string]bool{request: true}
	taskIDs := map[string]bool{}
	superseded := map[string]bool{}
	deliveries := map[string][]assistant.ExperimentDelivery{}
	records := map[string]*tasks.Record{}
	for {
		before := len(turns) + len(taskIDs)
		for _, m := range snap.Messages {
			t := m.Tool
			if t == nil || !turns[t.TurnID] || t.Phase != "completed" || len(t.Result) == 0 || agent.ToolResultFailed(t.Result) {
				continue
			}
			switch t.Name {
			case "create_task", "retry_task":
				var info protocol.TaskInfo
				if json.Unmarshal(t.Result, &info) == nil && core.Identifier(info.ID) {
					taskIDs[info.ID] = true
					if t.Name == "retry_task" {
						var args struct {
							TaskID string `json:"task_id"`
						}
						if json.Unmarshal(t.Args, &args) == nil && args.TaskID != info.ID {
							superseded[args.TaskID] = true
						}
					}
				}
			case "send_message":
				var mail protocol.SendMail
				if json.Unmarshal(t.Args, &mail) == nil && mail.To.Kind == "task" && mail.To.NodeID == s.options.RuntimeID {
					taskIDs[mail.To.AgentID] = true
				}
			}
		}
		if len(turns) > 512 || len(taskIDs) > 256 {
			return out, errors.New("execution observation limit exceeded")
		}
		for task := range taskIDs {
			if _, seen := deliveries[task]; !seen {
				record, e := s.Tasks.Get(ctx, owner, task)
				if e != nil {
					return out, e
				}
				if record == nil {
					return out, errors.New("associated task unavailable")
				}
				records[task] = record
				values, e := s.Mailbox.ExperimentDeliveries(ctx, owner, task, since)
				if e != nil {
					return out, e
				}
				deliveries[task] = values
			}
			for _, d := range deliveries[task] {
				if d.Turn != "" {
					turns[d.Turn] = true
				}
			}
		}
		if len(turns)+len(taskIDs) == before {
			break
		}
	}
	states := map[string]protocol.ChatReply{}
	for _, r := range protocol.ProjectChatReplies(snap.Messages, snap.Turn) {
		states[r.InputID] = r
		if r.InputID == out.MessageID {
			out.Assistant.State = r.State
		}
	}
	running := false
	blocked := ""
	failure := ""
	for turn := range turns {
		out.Turns = append(out.Turns, turn)
	}
	sort.Strings(out.Turns)
	for _, turn := range out.Turns {
		r, ok := states[inputs[turn]]
		if !ok {
			running = true
			continue
		}
		if snap.Turn != nil && snap.Turn.UserMessageID == inputs[turn] {
			out.Assistant.State = r.State
		}
		switch r.State {
		case "completed":
		case "waiting", "paused":
			blocked = r.State
		case "failed", "cancelled", "unknown":
			failure = r.State
			if r.Error != "" {
				out.Error = r.Error
			}
		default:
			running = true
		}
	}
	sort.Strings(out.Turns)
	inputSet := map[string]bool{}
	for t := range turns {
		inputSet[inputs[t]] = true
	}
	delete(inputSet, "")
	for _, m := range snap.Messages {
		if m.Tool != nil && turns[m.Tool.TurnID] && m.Status == protocol.Streaming {
			if snap.Turn != nil && snap.Turn.UserMessageID == inputs[m.Tool.TurnID] {
				out.Assistant.ActiveTools = append(out.Assistant.ActiveTools, m.Tool.Name)
			}
		}
	}
	sort.Strings(out.Assistant.ActiveTools)
	current := ""
	for _, m := range snap.Messages {
		if (m.Role == protocol.User && !m.Steering) || (m.Role == protocol.System && m.Compaction == nil) {
			current = m.ID
		}
		parent := m.ReplyTo
		if parent == "" {
			parent = current
		}
		if m.Role == protocol.Assistant && inputSet[parent] && m.Text != "" && m.Status == protocol.Complete {
			out.ReplyIDs = append(out.ReplyIDs, m.ID)
		}
	}
	ids := make([]string, 0, len(taskIDs))
	for id := range taskIDs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		record := records[id]
		progress := protocol.AgentExecutionProgress{ID: id, RunID: record.RunID, State: string(record.Info.Phase), ActiveTools: []string{}}
		if record.Info.Paused {
			progress.State = "paused"
		}
		if record.Info.RemoteWaiting {
			progress.State = "remote_waiting"
		}
		if record.Info.Phase.Scope() == "active" {
			var err error
			progress.ActiveTools, err = s.Usage.ActiveToolNames(ctx, id, record.RunID)
			if err != nil {
				return out, err
			}
		}
		out.Tasks = append(out.Tasks, progress)
		if (record.Execution != nil && !record.Execution.Notified) || record.Info.Phase == protocol.TaskRunning || record.Info.Phase == protocol.TaskQueued {
			running = true
		}
		if record.Info.RemoteWaiting || record.Info.Paused || record.Info.Phase == protocol.TaskWaiting || record.Info.Phase == protocol.TaskWaitingApproval {
			blocked = string(record.Info.Phase)
			if record.Info.Paused {
				blocked = "paused"
			}
			if record.Info.RemoteWaiting {
				blocked = "remote_waiting"
			}
		}
		if !superseded[id] && (record.Info.Phase == protocol.TaskFailed || record.Info.Phase == protocol.TaskCancelled || record.Info.Phase == protocol.TaskInterrupted) {
			failure = string(record.Info.Phase)
		}
		for _, d := range deliveries[id] {
			switch d.State {
			case "queued", "processing":
				running = true
			case "waiting":
				blocked = "waiting"
			case "failed", "interrupted":
				failure = d.State
			}
		}
	}
	sort.Slice(out.Tasks, func(i, j int) bool { return out.Tasks[i].ID < out.Tasks[j].ID })
	for _, approval := range s.Permissions.Pending(owner) {
		if turns[approval.Request.Turn] || taskIDs[approval.Request.Task] {
			blocked = "waiting_approval"
		}
	}
	switch {
	case blocked != "":
		out.State = blocked
	case running:
		out.State = "running"
	case failure != "":
		out.State = failure
	default:
		out.State = "completed"
	}
	out.Terminal = out.State == "completed" || out.State == "failed" || out.State == "cancelled" || out.State == "interrupted"
	return out, nil
}
