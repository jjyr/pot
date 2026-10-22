package agentlink

import (
	"context"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

type TaskGuidance struct {
	From      protocol.AgentRef `json:"from"`
	ID        string            `json:"id"`
	RequestID string            `json:"request_id"`
	Text      string            `json:"text"`
}
type TaskInputBackend interface {
	Input(context.Context, TaskGuidance) (Task, error)
}

func validGuidance(r TaskGuidance, node string) bool {
	return taskCaller(r.From) && r.From.NodeID == node && core.Identifier(r.ID) && core.Identifier(r.RequestID) && r.Text != "" && len(r.Text) <= 24000
}
func (s *Service) Input(ctx context.Context, link string, r TaskGuidance) (Task, error) {
	if !validGuidance(r, s.options.HostIdentity.NodeID) {
		return Task{}, ErrInvalid
	}
	c, err := s.Get(ctx, link)
	if err != nil {
		return Task{}, err
	}
	session, err := s.businessSession(ctx, c)
	if err != nil {
		return Task{}, err
	}
	var result Task
	err = s.call(ctx, session, "task.input", r, &result)
	if err == nil {
		err = validateTask(result, c.PeerHost.NodeID, r.ID, "")
	}
	return result, err
}
