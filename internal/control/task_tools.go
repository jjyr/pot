package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jjyr/pot/internal/schedule"
	"github.com/jjyr/pot/internal/tasks"
	"github.com/jjyr/pot/pkg/agent"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/modelprotocol"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
	"strconv"
	"time"
)

func (s *Service) productTools(worker bool) []modelprotocol.ToolDefinition {
	out := []modelprotocol.ToolDefinition{}
	if worker {
		return out
	}
	for _, raw := range core.ProductTools() {
		var d modelprotocol.ToolDefinition
		if core.DecodeJSON(raw, &d) == nil {
			out = append(out, d)
		}
	}
	return append(out, instructionTools()...)
}
func (s *Service) productDispatch(owner, turn string, generation uint64, worker bool) map[string]agent.Dispatch {
	return s.productDispatchBound(owner, owner, turn, generation, worker)
}
func (s *Service) productDispatchBound(owner, executionOwner, turn string, generation uint64, worker bool) map[string]agent.Dispatch {
	out := map[string]agent.Dispatch{}
	for _, d := range s.productTools(worker) {
		out[d.Name] = func(ctx context.Context, e core.ExecutionContext, call modelprotocol.ToolCall, emit func(core.Update) error) (json.RawMessage, error) {
			if e.Owner != executionOwner || generation == 0 {
				return nil, errors.New("stale product tool binding")
			}
			if call.Name == "read_agent_instructions" || call.Name == "update_agent_instructions" {
				return s.instructionCall(ctx, owner, turn, generation, call)
			}
			id, _ := core.HashJSON([]any{owner, turn, generation, call.ID})
			result, err := s.productCall(ctx, owner, id, call.Name, call.Arguments)
			if err != nil {
				return nil, err
			}
			return json.Marshal(result)
		}
	}
	return out
}
func (s *Service) productCall(ctx context.Context, owner, id, name string, args json.RawMessage) (any, error) {
	if s.Tasks == nil {
		return nil, errors.New("Task service unavailable")
	}
	var fields map[string]json.RawMessage
	if err := core.DecodeJSON(args, &fields); err != nil {
		return nil, err
	}
	field := func(key string) string { var v string; _ = json.Unmarshal(fields[key], &v); return v }
	inject := func() json.RawMessage {
		fields["request_id"], _ = json.Marshal(id)
		raw, _ := json.Marshal(fields)
		return raw
	}
	switch name {
	case "create_task":
		var request protocol.CreateTask
		if err := core.DecodeJSON(inject(), &request); err != nil {
			return nil, err
		}
		r, err := s.Tasks.Create(ctx, owner, request)
		if err == nil {
			s.wakeTasks()
		}
		return r.Info, err
	case "list_tasks":
		var q protocol.TaskQuery
		if err := core.DecodeJSON(args, &q); err != nil {
			return nil, err
		}
		return s.Tasks.Page(ctx, owner, q)
	case "get_task":
		r, err := s.Tasks.Get(ctx, owner, field("task_id"))
		if err != nil || r == nil {
			return nil, errors.New("Task missing")
		}
		return map[string]any{"task": r.Info, "error": r.Error, "result": r.Result}, nil
	case "guide_task":
		err := s.Tasks.Guide(ctx, owner, field("task_id"), id, field("text"))
		s.wakeTasks()
		return map[string]bool{"accepted": err == nil}, err
	case "send_message":
		err := s.Tasks.SendReference(ctx, owner, field("task_id"), id, owner, field("text"))
		s.wakeTasks()
		return map[string]bool{"accepted": err == nil}, err
	case "interrupt", "cancel_task":
		mode := "cancel"
		if name == "interrupt" {
			mode = "interrupt"
		}
		err := s.Tasks.Stop(ctx, owner, field("task_id"), mode)
		s.wakeTasks()
		return map[string]bool{"accepted": err == nil}, err
	case "wait_task":
		var q struct {
			IDs     []string          `json:"task_ids"`
			After   map[string]string `json:"after"`
			Timeout *uint64           `json:"timeout_ms"`
		}
		if err := core.DecodeJSON(args, &q); err != nil {
			return nil, err
		}
		timeout := uint64(30000)
		if q.Timeout != nil {
			timeout = *q.Timeout
		}
		if timeout > 60000 {
			return nil, errors.New("task wait exceeds 60 seconds")
		}
		targets := []tasks.WaitTarget{}
		for _, id := range q.IDs {
			after := uint64(0)
			if text := q.After[id]; text != "" {
				var err error
				after, err = strconv.ParseUint(text, 10, 64)
				if err != nil {
					return nil, err
				}
			}
			targets = append(targets, tasks.WaitTarget{TaskID: id, After: after})
		}
		wait, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Millisecond)
		defer cancel()
		items, err := s.Tasks.Wait(wait, owner, targets)
		if err != nil {
			return nil, err
		}
		out := []any{}
		cursors := map[string]string{}
		for _, item := range items {
			out = append(out, map[string]any{"task": item.Info, "error": item.Error})
			cursors[item.Info.ID] = strconv.FormatUint(item.Cursor, 10)
		}
		return map[string]any{"tasks": out, "cursors": cursors, "timed_out": wait.Err() != nil}, nil
	case "list_schedules":
		var q struct {
			Scope  string `json:"scope"`
			Offset uint64 `json:"offset"`
			Limit  uint64 `json:"limit"`
		}
		if err := core.DecodeJSON(args, &q); err != nil {
			return nil, err
		}
		return s.Schedules.Page(ctx, owner, schedule.Query{Scope: q.Scope, Offset: q.Offset, Limit: q.Limit})
	case "get_schedule":
		var q struct {
			ID     string `json:"schedule_id"`
			Offset uint64 `json:"offset"`
			Limit  uint64 `json:"limit"`
		}
		if err := core.DecodeJSON(args, &q); err != nil {
			return nil, err
		}
		return s.Schedules.Page(ctx, owner, schedule.Query{Selected: q.ID, RunsOffset: q.Offset, Limit: q.Limit})
	case "manage_schedule":
		var request schedule.Request
		if err := core.DecodeJSON(inject(), &request); err != nil {
			return nil, err
		}
		result, err := s.Schedules.Manage(ctx, owner, request, storage.Now())
		if err == nil {
			s.wakeTasks()
			s.Changes.Notify("schedules")
		}
		return result, err
	case "list_execution_targets":
		machines, err := s.Machines.Machines(ctx)
		if err != nil {
			return nil, err
		}
		policy, _, err := s.Permissions.Policy(s.options.RuntimeID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"local": map[string]any{"runtime_id": s.options.RuntimeID, "policy": policy}, "machines": machines}, nil
	case "list_assistants":
		directory, err := s.MailDirectory(ctx)
		return map[string]any{"assistants": directory.Assistants}, err
	case "send_assistant_message":
		var send protocol.SendMail
		if err := core.DecodeJSON(args, &send); err != nil {
			return nil, err
		}
		result, err := s.sendAssistantMail(ctx, id, owner, send)
		if err == nil {
			s.wakeTasks()
			s.Changes.Notify("assistant-mail")
		}
		return result, err
	case "get_model_catalog":
		var query ModelCatalogQuery
		if err := core.DecodeJSON(args, &query); err != nil {
			return nil, err
		}
		return s.ModelCatalog(query)
	}
	return nil, fmt.Errorf("unknown product tool %s", name)
}
func (s *Service) MailDirectory(ctx context.Context) (protocol.Directory, error) {
	definitions, err := s.Registry.Definitions(ctx)
	d := protocol.Directory{ControlID: s.options.RuntimeID, Assistants: []protocol.MailAssistant{}}
	if err != nil {
		return d, err
	}
	for _, v := range definitions {
		d.Assistants = append(d.Assistants, protocol.MailAssistant{Address: protocol.AssistantRef{ControlID: s.options.RuntimeID, AssistantID: v.ID}, Name: v.Name})
	}
	return d, nil
}
