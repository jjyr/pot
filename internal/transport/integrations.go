package transport

import (
	"context"
	"errors"
	"strings"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/integrations"
	"github.com/jjyr/pot/pkg/protocol"
)

func (a *Application) integrationCall(ctx context.Context, method, path string, r Request) (any, error, bool) {
	const base = "/api/v1/integrations"
	if path != base && !strings.HasPrefix(path, base+"/") {
		return nil, nil, false
	}
	if path == base {
		if method != "GET" {
			return nil, protocol.APIError{Code: "method_not_allowed", Message: "GET required"}, true
		}
		items, err := a.Control.Integrations.List(ctx)
		return map[string]any{"items": items}, err, true
	}
	parts := strings.Split(strings.TrimPrefix(path, base+"/"), "/")
	if len(parts) < 2 || !core.Identifier(parts[0]) {
		return nil, integrations.ErrNotFound, true
	}
	service := parts[0]
	var result any
	var err error
	switch {
	case service == "codex" && len(parts) == 2 && parts[1] == "connection" && method == "POST":
		result, err = a.Control.PairCodex(ctx, r.Body)
	case len(parts) == 2 && parts[1] == "login" && method == "POST":
		var command struct{}
		err = decode(r.Body, &command)
		if err == nil {
			result, err = a.Control.Integrations.BeginLogin(ctx, service)
		}
	case len(parts) == 3 && parts[1] == "login" && core.Identifier(parts[2]) && method == "GET":
		result, err = a.Control.Integrations.Login(ctx, service, parts[2])
	case len(parts) == 3 && parts[1] == "login" && core.Identifier(parts[2]) && method == "DELETE":
		err = a.Control.Integrations.CancelLogin(ctx, service, parts[2])
		result = map[string]bool{"cancelled": err == nil}
	case len(parts) == 2 && parts[1] == "connection" && method == "DELETE":
		var command struct {
			ExpectedRevision uint64 `json:"expected_revision"`
		}
		err = decode(r.Body, &command)
		if err == nil {
			err = a.Control.Integrations.Disconnect(ctx, service, command.ExpectedRevision)
		}
		result = map[string]bool{"disconnected": err == nil}
	default:
		err = integrations.ErrNotFound
	}
	if errors.Is(err, integrations.ErrNotFound) {
		err = protocol.APIError{Code: "not_found", Message: "integration or login session not found"}
	}
	return result, err, true
}
