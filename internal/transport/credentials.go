package transport

import (
	"context"
	"errors"
	"github.com/jjyr/pot/internal/credentials"
)

func (a *Application) credentialCall(ctx context.Context, method, path string, r Request) (any, error, bool) {
	if path != "/api/v1/credentials" && path != "/api/v1/credentials/remove" {
		return nil, nil, false
	}
	if path == "/api/v1/credentials" && method == "GET" {
		items, err := a.Control.Credentials.List(ctx)
		return map[string]any{"items": items}, err, true
	}
	if method != "POST" {
		return nil, errors.New("POST required"), true
	}
	if path == "/api/v1/credentials/remove" {
		var command credentials.RemoveRequest
		if err := decode(r.Body, &command); err != nil {
			return nil, err, true
		}
		err := a.Control.RemoveCredentialAccount(ctx, command)
		return map[string]bool{"removed": err == nil}, err, true
	}
	var command credentials.SaveRequest
	if err := decode(r.Body, &command); err != nil {
		return nil, err, true
	}
	account, err := a.Control.SaveCredentialAccount(ctx, command)
	return account, err, true
}
