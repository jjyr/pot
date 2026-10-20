package transport

import (
	"context"
	"github.com/jjyr/pot/internal/control"
	"github.com/jjyr/pot/pkg/protocol"
)

func (a *Application) agentLinkCall(ctx context.Context, method, path, owner string, r Request) (any, error, bool) {
	if path != "/api/v1/agent-links" && path != "/api/v1/agent-links/invitation" {
		return nil, nil, false
	}
	if method == "GET" && path == "/api/v1/agent-links" {
		result, err := a.Control.AgentLinks(ctx, owner)
		return result, err, true
	}
	if method != "POST" {
		return nil, protocol.APIError{Code: "method_not_allowed"}, true
	}
	var command control.AgentLinkCommand
	if err := decode(r.Body, &command); err != nil {
		return nil, err, true
	}
	if command.AssistantID == "" {
		command.AssistantID = owner
	}
	if path == "/api/v1/agent-links/invitation" {
		command.Action = "invite"
	}
	result, err := a.Control.ManageAgentLink(ctx, command)
	return result, err, true
}
