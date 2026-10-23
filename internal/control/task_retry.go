package control

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/modelprotocol"
)

var retryTaskTool = modelprotocol.ToolDefinition{
	Name:        "retry_task",
	Description: "Continue a delivered failed task after diagnosing its failure. Supply a repair approach within the original goal and authority. Runtime preserves the original instructions, additional guidance, machine, model, skills and requirements. A new workspace is used; previous files are not imported. Inspect uncertainty and partial effects first. Never retry an owner-denied operation or cancelled/interrupted work. Repeated calls for the same source return the same replacement. A failed replacement can be recovered after its own result is delivered. Remain silent after acceptance unless other delivered outcomes need a substantive reply.",
	InputSchema: json.RawMessage(`{"type":"object","properties":{"task_id":{"type":"string"},"instructions":{"type":"string","minLength":1,"maxLength":8192}},"required":["task_id","instructions"],"additionalProperties":false}`),
}

// Result text cannot nominate recovery work. Authorization is bound to the
// authenticated owner's currently claimed, durable Runtime delivery batch.
func (s *Service) deliveredTask(ctx context.Context, owner, turn, task string) error {
	if !strings.HasPrefix(turn, "pot-result-") {
		return errors.New("task recovery requires a Runtime result delivery")
	}
	var bound bool
	err := s.Tasks.DB().QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM agent_messages m JOIN agent_delivery_items d ON d.message=m.id
 WHERE d.batch=? AND m.recipient=? AND m.state='processing' AND json_extract(m.envelope,'$.kind')='task_result'
 AND json_extract(m.envelope,'$.result.task_id')=?)`, strings.TrimPrefix(turn, "pot-result-"), owner, task).Scan(&bound)
	if err != nil {
		return err
	}
	if !bound {
		return errors.New("task is outside the current result delivery")
	}
	return nil
}

func (s *Service) retryDeliveredTask(ctx context.Context, owner, turn string, args json.RawMessage) (json.RawMessage, error) {
	var request struct {
		TaskID       string `json:"task_id" required:"true"`
		Instructions string `json:"instructions" required:"true"`
	}
	if err := core.DecodeJSON(args, &request); err != nil {
		return nil, err
	}
	if err := s.deliveredTask(ctx, owner, turn, request.TaskID); err != nil {
		return nil, err
	}
	body, _ := json.Marshal(map[string]string{"instructions": request.Instructions})
	info, err := s.CallAgent(ctx, owner, agentlink.TaskRetry, agentlink.Call{ID: request.TaskID, Body: body})
	if err != nil {
		return nil, err
	}
	s.wakeTasks()
	return json.Marshal(info)
}

func (s *Service) resultRetryTools(ctx context.Context, owner, turn string) ([]modelprotocol.ToolDefinition, error) {
	tools := []modelprotocol.ToolDefinition{}
	rows, err := s.Tasks.DB().QueryContext(ctx, `SELECT json_extract(m.envelope,'$.result.task_id') FROM agent_messages m
 JOIN agent_delivery_items d ON d.message=m.id WHERE d.batch=? AND m.recipient=? AND m.state='processing'
 AND json_extract(m.envelope,'$.kind')='task_result' AND json_extract(m.envelope,'$.result.phase')='failed'`, strings.TrimPrefix(turn, "pot-result-"), owner)
	if err != nil {
		return nil, err
	}
	var tasks []string
	for rows.Next() {
		var task string
		if err := rows.Scan(&task); err != nil {
			rows.Close()
			return nil, err
		}
		tasks = append(tasks, task)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, task := range tasks {
		eligible, err := s.Tasks.CanRetry(ctx, owner, task)
		if err != nil {
			return nil, err
		}
		if eligible {
			return append(tools, retryTaskTool), nil
		}
	}
	return tools, nil
}
