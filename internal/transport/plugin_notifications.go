package transport

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/jjyr/pot/pkg/protocol"
)

func (a *Application) pluginNotificationsCall(ctx context.Context, method, path string, r Request) (any, error, bool) {
	const base = "/api/v1/plugin-notifications"
	if path != base && !strings.HasPrefix(path, base+"/") {
		return nil, nil, false
	}
	if path == base && method == "GET" {
		result, err := a.Control.PluginNotifications(ctx)
		return result, err, true
	}
	if strings.HasPrefix(path, base+"/") && method == "POST" {
		var command struct {
			Tool      string          `json:"tool"`
			Arguments json.RawMessage `json:"arguments"`
		}
		err := decode(r.Body, &command)
		var result any
		if err == nil {
			result, err = a.Control.PluginNotificationCall(ctx, strings.TrimPrefix(path, base+"/"), command.Tool, command.Arguments)
		}
		if err != nil {
			return nil, protocol.APIError{Code: "plugin_configuration", Message: err.Error()}, true
		}
		return result, err, true
	}
	return nil, protocol.APIError{Code: "not_found", Message: "plugin notification route unavailable"}, true
}
