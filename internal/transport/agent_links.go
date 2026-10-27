package transport

import (
	"context"
	"github.com/jjyr/pot/internal/control"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/protocol"
	"net/url"
)

func (a *Application) agentLinkCall(ctx context.Context, method, path string, r Request) (any, error, bool) {
	u, _ := url.ParseRequestURI(r.Path)
	if path == "/api/v1/agent-links/mcp" {
		if method == "GET" {
			value, err := a.Control.PotMCPConnectionView(ctx)
			return value, err, true
		}
		if method != "POST" {
			return nil, protocol.APIError{Code: "method_not_allowed"}, true
		}
		var input control.PotMCPConnectionCommand
		if err := decode(r.Body, &input); err != nil {
			return nil, err, true
		}
		value, err := a.Control.ManagePotMCPConnection(ctx, input)
		return value, err, true
	}

	if path == "/api/v1/agent-links/assistants" && method == "POST" {
		var input struct {
			ConnectionID string            `json:"connection_id"`
			From         protocol.AgentRef `json:"from,omitempty"`
			Action       string            `json:"action"`
			RequestID    string            `json:"request_id,omitempty"`
			ID           string            `json:"id,omitempty"`
			Name         *string           `json:"name,omitempty"`
			Model        *string           `json:"model,omitempty"`
			Effort       *string           `json:"effort,omitempty"`
			Workspace    string            `json:"workspace,omitempty"`
			Revision     uint64            `json:"revision,omitempty"`
		}
		if err := decode(r.Body, &input); err != nil {
			return nil, err, true
		}
		value, err := a.Control.ManageLinkedAssistant(ctx, input.ConnectionID, agentlink.AssistantManagementRequest{Action: input.Action, RequestID: input.RequestID, ID: input.ID, Name: input.Name, Model: input.Model, Effort: input.Effort, Workspace: input.Workspace, Revision: input.Revision})
		return value, err, true
	}
	if path == "/api/v1/agent-links/capabilities" && method == "GET" {
		result, err := a.Control.AgentCapabilities(ctx, u.Query().Get("assistant_id"))
		return result, err, true
	}
	if path != "/api/v1/agent-links" && path != "/api/v1/agent-links/invitation" {
		return nil, nil, false
	}
	if method == "GET" && path == "/api/v1/agent-links" {
		result, err := a.Control.AgentLinks(ctx)
		return result, err, true
	}
	if method != "POST" {
		return nil, protocol.APIError{Code: "method_not_allowed"}, true
	}
	var command control.AgentLinkCommand
	if err := decode(r.Body, &command); err != nil {
		return nil, err, true
	}
	if path == "/api/v1/agent-links/invitation" {
		command.Action = "invite"
	}
	result, err := a.Control.ManageAgentLink(ctx, command)
	return result, err, true
}
