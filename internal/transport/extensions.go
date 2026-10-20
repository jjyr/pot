package transport

import (
	"context"
	"github.com/jjyr/pot/internal/control"
	"github.com/jjyr/pot/pkg/protocol"
	"strings"
)

func (a *Application) extensionCall(ctx context.Context, method, path, owner string, r Request) (any, error, bool) {
	if path != "/api/v1/extensions" && !strings.HasPrefix(path, "/api/v1/extensions/") {
		return nil, nil, false
	}
	var result any
	var err error
	switch {
	case method == "GET" && path == "/api/v1/extensions":
		result, err = a.Control.Extensions(ctx, owner)
	case method == "GET" && strings.HasPrefix(path, "/api/v1/extensions/skills/"):
		result, err = a.Control.ExtensionContent(ctx, owner, strings.TrimPrefix(path, "/api/v1/extensions/skills/"))
	case method == "POST" && path == "/api/v1/extensions":
		var command control.ExtensionCommand
		err = decode(r.Body, &command)
		if err == nil {
			result, err = a.Control.ManageExtension(ctx, owner, command)
		}
	case method == "POST" && (path == "/api/v1/extensions/mcp/oauth/begin" || path == "/api/v1/extensions/mcp/oauth/complete"):
		var command control.MCPOAuthCommand
		err = decode(r.Body, &command)
		if err == nil {
			if strings.HasSuffix(path, "/begin") {
				result, err = a.Control.BeginMCPOAuth(ctx, command)
			} else {
				result, err = a.Control.CompleteMCPOAuth(ctx, command)
			}
		}
	case method == "POST" && path == "/api/v1/extensions/mcp":
		var command control.MCPCommand
		err = decode(r.Body, &command)
		if err == nil {
			result, err = a.Control.ManageMCP(ctx, command)
		}
	default:
		err = protocol.APIError{Code: "not_found", Message: "extension route unavailable"}
	}
	if err != nil {
		return nil, protocol.APIError{Code: "extension_configuration", Message: err.Error()}, true
	}
	return result, nil, true
}
