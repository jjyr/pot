package transport

import (
	"context"
	"github.com/jjyr/pot/internal/control"
	"github.com/jjyr/pot/pkg/protocol"
	"net/url"
)

func (a *Application) agentLinkCall(ctx context.Context, method, path string, r Request) (any, error, bool) {
	u, _ := url.ParseRequestURI(r.Path)
	if path == "/api/v1/agent-links/chat" {
		link, assistant := u.Query().Get("link_id"), u.Query().Get("assistant_id")
		if method == "GET" {
			result, err := a.Control.LinkChat(ctx, link, assistant, u.Query().Get("request_id"))
			return result, err, true
		}
		if method == "POST" {
			var input struct {
				RequestID string `json:"request_id"`
				Text      string `json:"text"`
			}
			if err := decode(r.Body, &input); err != nil {
				return nil, err, true
			}
			result, err := a.Control.SendLinkChat(ctx, link, assistant, input.RequestID, input.Text)
			return result, err, true
		}
		return nil, protocol.APIError{Code: "method_not_allowed"}, true
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
