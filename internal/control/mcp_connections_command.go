package control

import (
	"context"
	"github.com/jjyr/pot/pkg/agentlink"
)

type PotMCPConnectionCommand struct {
	Action      string `json:"action"`
	ID          string `json:"id,omitempty"`
	URL         string `json:"url,omitempty"`
	RedirectURI string `json:"redirect_uri,omitempty"`
	State       string `json:"state,omitempty"`
	Code        string `json:"code,omitempty"`
}

// ManagePotMCPConnection is an owner UI operation, never an MCP collaboration
// tool. Browser authorization is enforced by the existing owner transport.
func (s *Service) ManagePotMCPConnection(ctx context.Context, input PotMCPConnectionCommand) (any, error) {
	switch input.Action {
	case "authorize":
		return s.BeginPotMCPConnection(ctx, input.URL, input.RedirectURI, input.ID)
	case "complete":
		c, e := s.CompletePotMCPConnection(ctx, input.State, input.Code)
		if e == nil {
			s.startPotMCP(c.ID)
		}
		return c, e
	case "disconnect":
		if e := s.DisconnectPotMCP(ctx, input.ID); e != nil {
			return nil, e
		}
	case "reconnect":
		if e := s.ReconnectPotMCP(ctx, input.ID); e != nil {
			return nil, e
		}
	case "revoke":
		if e := s.RevokePotMCP(ctx, input.ID); e != nil {
			return nil, e
		}
	default:
		return nil, agentlink.ErrInvalid
	}
	return s.PotMCPConnection(ctx, input.ID)
}
