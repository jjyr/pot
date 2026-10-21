package transport

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/jjyr/pot/internal/assistant"
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
			Enabled   bool            `json:"enabled"`
			Assistant string          `json:"assistant"`
			Config    json.RawMessage `json:"config"`
			RequestID string          `json:"request_id"`
			Revision  int64           `json:"revision"`
		}
		err := decode(r.Body, &command)
		if err == nil {
			err = a.Control.ConfigurePluginNotifications(ctx, command.RequestID, assistant.BackgroundBinding{ID: strings.TrimPrefix(path, base+"/"), Enabled: command.Enabled, Assistant: command.Assistant, Config: command.Config, Revision: command.Revision})
		}
		if err != nil {
			return nil, protocol.APIError{Code: "plugin_configuration", Message: err.Error()}, true
		}
		result, err := a.Control.PluginNotifications(ctx)
		return result, err, true
	}
	return nil, protocol.APIError{Code: "not_found", Message: "plugin notification route unavailable"}, true
}
