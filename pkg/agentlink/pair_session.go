package agentlink

import (
	"context"
	"encoding/json"

	"github.com/jjyr/pot/pkg/peer"
)

func (s *Service) pairHandler(metadata pairRequest, remote peer.Identity) peer.Handler {
	return func(ctx context.Context, input peer.Request) (json.RawMessage, error) {
		if input.Method != "pair" {
			return s.handler(metadata.ID, remote)(ctx, input)
		}
		var params struct {
			Code string `json:"code"`
		}
		if len(input.Params) > 128 || decodeJSON(input.Params, &params) != nil || !validToken(params.Code) {
			return nil, ErrInvalid
		}
		request := metadata
		request.Code = params.Code
		connection, err := s.accept(ctx, request, remote)
		if err != nil {
			return nil, err
		}
		s.activatePending(metadata.ID, remote)
		return json.Marshal(connection)
	}
}

func (s *Service) activatePending(id string, remote peer.Identity) {
	s.mu.Lock()
	sessions := make([]*peer.Session, 0, len(s.pendingSessions))
	for session := range s.pendingSessions {
		var auth sessionAuth
		if session.Remote() == remote && decodeJSON(session.Auth(), &auth) == nil && auth.ID == id {
			sessions = append(sessions, session)
		}
	}
	s.mu.Unlock()
	for _, session := range sessions {
		s.Attach(session)
	}
}
