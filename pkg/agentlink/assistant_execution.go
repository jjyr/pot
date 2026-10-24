package agentlink

import (
	"context"
	"github.com/jjyr/pot/pkg/protocol"
)

const AssistantManagement Operation = "assistant.management"

type ExecutionModel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type ExecutionWorkspace struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type ManagedAssistant struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Model     string `json:"model"`
	Effort    string `json:"effort"`
	Workspace string `json:"workspace"`
	Revision  uint64 `json:"revision"`
}
type AssistantManagementRequest struct {
	From      protocol.AgentRef `json:"from"`
	Action    string            `json:"action"`
	RequestID string            `json:"request_id,omitempty"`
	ID        string            `json:"id,omitempty"`
	Name      string            `json:"name,omitempty"`
	Model     string            `json:"model,omitempty"`
	Effort    string            `json:"effort,omitempty"`
	Workspace string            `json:"workspace,omitempty"`
	Revision  uint64            `json:"revision,omitempty"`
}
type AssistantManagementResult struct {
	Models     []ExecutionModel     `json:"models"`
	Workspaces []ExecutionWorkspace `json:"workspaces"`
	Assistants []ManagedAssistant   `json:"assistants"`
	Assistant  *ManagedAssistant    `json:"assistant,omitempty"`
}
type AssistantManagementBackend interface {
	ManageAssistant(context.Context, AssistantManagementRequest) (AssistantManagementResult, error)
}

func (s *Service) ManageAssistant(ctx context.Context, link string, input AssistantManagementRequest) (AssistantManagementResult, error) {
	if !s.enterRPC() {
		return AssistantManagementResult{}, ErrUnavailable
	}
	defer s.rpcWG.Done()
	if !input.From.Valid() || input.From.Kind != "user" || input.From.NodeID != s.options.HostIdentity.NodeID {
		return AssistantManagementResult{}, ErrUnauthorized
	}
	c, err := s.Get(ctx, link)
	if err != nil {
		return AssistantManagementResult{}, err
	}
	var result AssistantManagementResult
	err = s.callConnection(ctx, c, string(AssistantManagement), input, &result)
	if err == nil && input.Action != "catalog" {
		_, _ = s.Directory(ctx, link)
	}
	return result, err
}
