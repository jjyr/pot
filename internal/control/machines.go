package control

import (
	"context"
	"runtime"
)

func (s *Service) ControlOrigin() string {
	if s.agentLinks == nil {
		return ""
	}
	s.agentLinks.mu.Lock()
	defer s.agentLinks.mu.Unlock()
	return s.agentLinks.config.Endpoint
}

func (s *Service) localMachine(_ context.Context) (map[string]any, error) {
	policy, _, err := s.Permissions.Policy(s.options.RuntimeID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": s.options.RuntimeID, "runtime_id": s.options.RuntimeID, "name": s.options.RuntimeID, "os": runtime.GOOS, "connected": true, "role": "node", "tags": s.Machines.LocalLabels(), "policy": policy}, nil
}
func (s *Service) MachineList(ctx context.Context) ([]map[string]any, error) {
	local, err := s.localMachine(ctx)
	if err != nil {
		return nil, err
	}
	items := []map[string]any{local}
	return items, nil
}
