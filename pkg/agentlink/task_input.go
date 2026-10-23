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
	if link == "" {
		directory, err := s.localDirectory(ctx)
		if err != nil {
			return Task{}, err
		}
		result, err := s.input(ctx, directory, r)
		if err == nil {
			err = validateTask(result, s.options.HostIdentity.NodeID, directory.Adapter, r.ID, "")
		}
		return result, err
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
		err = validateTask(result, c.PeerHost.NodeID, c.Peer.Adapter, r.ID, "")
	}
	return result, err
}

// Both in-process and peer dispatch use the receiver's operation contract. Task
// ownership remains enforced by Status/Input on the original authenticated caller.
func (s *Service) input(ctx context.Context, directory Directory, r TaskGuidance) (Task, error) {
	backend, ok := s.options.Backend.(TaskInputBackend)
	if !ok {
		return Task{}, ErrUnsupported
	}
	task, err := s.options.Backend.Status(ctx, r.From, r.ID)
	if err != nil {
		return Task{}, err
	}
	capabilities := directory.Operations[task.Assistant.Address.AgentID]
	if task.Execution != nil {
		capabilities = Description{}
		if directory.TaskExecutor != nil {
			capabilities = *directory.TaskExecutor
		}
	}
	if !capabilities.Supports(TaskInput) {
		return Task{}, ErrUnsupported
	}
	return backend.Input(ctx, r)
}
