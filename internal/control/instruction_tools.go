package control

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/modelprotocol"
)

func instructionTools() []modelprotocol.ToolDefinition {
	return []modelprotocol.ToolDefinition{
		{Name: "read_agent_instructions", Description: "Read this assistant's instruction text and revision. Workers cannot access assistant rules.", InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)},
		{Name: "update_agent_instructions", Description: "Propose a full replacement of this assistant's instructions. Requires exact owner review independently of Host machine policy. A concurrent edit invalidates the reviewed revision.", InputSchema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string","maxLength":262144}},"required":["text"],"additionalProperties":false}`)},
	}
}
func (s *Service) instructionCall(ctx context.Context, owner, turn string, generation uint64, call modelprotocol.ToolCall) (json.RawMessage, error) {
	c, err := s.Conversation(ctx, owner)
	if err != nil {
		return nil, err
	}
	thread, err := c.Thread(ctx)
	if err != nil {
		return nil, err
	}
	if thread == nil || thread.Generation != generation {
		return nil, errors.New("instruction tool generation expired")
	}
	if call.Name == "read_agent_instructions" {
		var body struct{}
		if err = core.DecodeJSON(call.Arguments, &body); err != nil {
			return nil, err
		}
		rules, err := c.Instructions(ctx)
		if err != nil {
			return nil, err
		}
		return json.Marshal(rules)
	}
	var body struct {
		Text string `json:"text" required:"true"`
	}
	if err = core.DecodeJSON(call.Arguments, &body); err != nil {
		return nil, err
	}
	id, err := core.HashJSON([]any{owner, turn, generation, call.ID})
	if err != nil {
		return nil, err
	}
	pending, err := c.PrepareInstructionUpdate(ctx, id, body.Text)
	if err != nil {
		return nil, err
	}
	if pending.Result != nil {
		return json.Marshal(pending.Result)
	}
	_, epoch, err := s.Permissions.Policy(s.options.RuntimeID)
	if err != nil {
		return nil, err
	}
	review, _ := json.Marshal(map[string]string{"text": pending.Text, "before": pending.Before.Text, "revision": pending.Before.Revision})
	if _, err = s.Permissions.Authorize(ctx, HostRequest{Owner: owner, Turn: turn, Generation: generation, CallID: call.ID, MachineID: s.options.RuntimeID, EnrollmentEpoch: epoch, Tool: call.Name, Args: review}); err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	rules, err := c.CommitInstructionUpdate(ctx, id)
	if err != nil {
		return nil, err
	}
	s.Changes.Notify("instructions")
	return json.Marshal(rules)
}
