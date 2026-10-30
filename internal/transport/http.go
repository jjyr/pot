package transport

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/internal/localui"
	"github.com/jjyr/pot/internal/mcppublic"
	"github.com/jjyr/pot/pkg/protocol"
	"io"
	"net/http"
	"strings"
)

// Handler is the protected operator/client API. Browser publication uses its
// separate authenticated server adapter and explicit route allowlist.
func Handler(app *Application, token string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		expected := "Bearer " + token
		if token == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte(expected)) != 1 {
			writeError(w, http.StatusUnauthorized, protocol.APIError{Code: "unauthorized", Message: "Runtime authentication required"})
			return
		}
		if expectedID := r.Header.Get("X-Pot-Control-ID"); expectedID != "" && expectedID != app.RuntimeID {
			writeError(w, http.StatusConflict, protocol.APIError{Code: "identity_changed", Message: "Control identity changed"})
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/ws" {
			localui.ServeWebSocket(w, r, localui.SocketOptions{ExpectedControlID: app.RuntimeID, Caller: localui.Functions{
				Request: func(ctx context.Context, method, path string, body json.RawMessage) (json.RawMessage, error) {
					result, err := app.Call(ctx, Request{Method: method, Path: path, Body: body})
					if err != nil {
						return nil, err
					}
					return json.Marshal(result)
				},
				Events: app.Stream,
			}})
			return
		}

		if app.Maintenance != nil && app.Maintenance() && r.URL.Path != "/api/v1/updates" && r.URL.Path != "/api/v1/runtime" && r.URL.Path != "/api/v1/control" {
			writeError(w, http.StatusServiceUnavailable, protocol.APIError{Code: "update_maintenance"})
			return
		}
		if mcppublic.Route(r.URL.Path) {
			app.Control.ServePublishedMCP(w, r)
			return
		}
		if app.terminalSocket(w, r) {
			return
		}
		if app.resourceContent(w, r) {
			return
		}
		if app.download(w, r) {
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodPost && !(r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/v1/integrations/")) {
			writeError(w, http.StatusMethodNotAllowed, protocol.APIError{Code: "method_not_allowed", Message: "method not allowed"})
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, controlBodyLimit))
		if err != nil {
			writeError(w, http.StatusRequestEntityTooLarge, protocol.APIError{Code: "body_limit", Message: "request body exceeds limit"})
			return
		}
		result, err := app.Call(r.Context(), Request{r.Method, r.URL.RequestURI(), body})
		if err != nil {
			status := http.StatusBadRequest
			var api protocol.APIError
			if errors.As(err, &api) {
				switch api.Code {
				case "not_found":
					status = http.StatusNotFound
				case "method_not_allowed":
					status = http.StatusMethodNotAllowed
				case "pot_token_limit":
					status = http.StatusTooManyRequests
				default:
					status = http.StatusConflict
				}
			} else {
				api = protocol.APIError{Code: "request_failed", Message: err.Error()}
			}
			writeError(w, status, api)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if err = json.NewEncoder(w).Encode(result); err != nil {
			return
		}
	})
}

const controlBodyLimit = 16 * 1024 * 1024

func writeError(w http.ResponseWriter, status int, err protocol.APIError) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(err)
}
