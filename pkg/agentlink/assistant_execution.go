package agentlink

import (
	"context"
	"errors"
	"github.com/jjyr/pot/pkg/protocol"
)

const AssistantManagement Operation = "assistant.management"

var ErrModelUnavailable = errors.New("assistant_model_unavailable")
var ErrWorkspaceUnavailable = errors.New("assistant_workspace_unavailable")

type ManagementField struct {
	Name     string `json:"name"`
	Required bool   `json:"required"`
	Editable bool   `json:"editable"`
	Default  string `json:"default,omitempty"`
}
type ManagementRuntime struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

type ExecutionModel struct {
	Efforts       []string `json:"efforts,omitempty"`
	DefaultEffort string   `json:"default_effort,omitempty"`
	ID            string   `json:"id"`
	Name          string   `json:"name"`
}
type ExecutionWorkspace struct {
	Description string `json:"description"`
	Mode        string `json:"mode"`
	ID          string `json:"id"`
	Name        string `json:"name"`
}
type ManagedAssistant struct {
	Avatar       string            `json:"avatar"`
	Capabilities Description       `json:"capabilities"`
	Address      protocol.AgentRef `json:"address"`
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Model        string            `json:"model"`
	Effort       string            `json:"effort"`
	Workspace    string            `json:"workspace"`
	Revision     uint64            `json:"revision"`
}
type AssistantManagementRequest struct {
	From      protocol.AgentRef `json:"from"`
	Action    string            `json:"action"`
	RequestID string            `json:"request_id,omitempty"`
	ID        string            `json:"id,omitempty"`
	Name      *string           `json:"name,omitempty"`
	Model     *string           `json:"model,omitempty"`
	Effort    *string           `json:"effort,omitempty"`
	Workspace string            `json:"workspace,omitempty"`
	Revision  uint64            `json:"revision,omitempty"`
}
type AssistantManagementResult struct {
	SchemaVersion int                  `json:"schema_version"`
	Runtime       ManagementRuntime    `json:"runtime"`
	Actions       []string             `json:"actions"`
	Fields        []ManagementField    `json:"fields"`
	Models        []ExecutionModel     `json:"models"`
	Workspaces    []ExecutionWorkspace `json:"workspaces"`
	Assistants    []ManagedAssistant   `json:"assistants"`
	Assistant     *ManagedAssistant    `json:"assistant,omitempty"`
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
	if err == nil {
		valid := func(value ManagedAssistant) bool {
			return value.Address.Valid() && value.Address.NodeID == c.PeerHost.NodeID && value.Address.Kind == "assistant" && value.Address.AgentID == value.ID && value.Revision > 0
		}
		for _, value := range result.Assistants {
			if !valid(value) {
				return AssistantManagementResult{}, ErrConflict
			}
		}
		if input.Action != "catalog" && (result.Assistant == nil || !valid(*result.Assistant) || (input.Action == "update" && result.Assistant.Address.AgentID != input.ID)) {
			return AssistantManagementResult{}, ErrConflict
		}
	}
	if err == nil && input.Action != "catalog" {
		_, _ = s.Directory(ctx, link)
	}
	return result, err
}
