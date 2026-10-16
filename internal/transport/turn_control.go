package transport

import (
	"context"
	"github.com/jjyr/pot/internal/assistant"
	"github.com/jjyr/pot/internal/control"
)

func (a *Application) turnControlCall(ctx context.Context, method, path, owner string, r Request) (any, error, bool) {
	switch path {
	case "/api/v1/usage/providers":
		if method == "GET" {
			result, err := a.Control.ProviderAllowances(ctx)
			return result, err, true
		}
		var body control.SaveProviderAllowance
		if err := decode(r.Body, &body); err != nil {
			return nil, err, true
		}
		result, err := a.Control.SaveProviderAllowance(ctx, body)
		return result, err, true
	case "/api/v1/turn/steer", "/api/v1/turn/control":
		if method != "POST" {
			return nil, nil, false
		}
		c, err := a.Control.Conversation(ctx, owner)
		if err != nil {
			return nil, err, true
		}
		if path == "/api/v1/turn/steer" {
			var body assistant.SteerRequest
			if err = decode(r.Body, &body); err != nil {
				return nil, err, true
			}
			result, err := c.Steer(ctx, body)
			return result, err, true
		}
		var body assistant.TurnControl
		if err = decode(r.Body, &body); err == nil {
			err = c.Control(ctx, body)
		}
		return map[string]bool{"accepted": err == nil}, err, true
	}
	return nil, nil, false
}
