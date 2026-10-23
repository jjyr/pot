package transport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/terminal"
)

func terminalPath(path string) bool {
	return path == "/api/v1/terminals" || strings.HasPrefix(path, "/api/v1/terminals/")
}
func (a *Application) terminalCall(ctx context.Context, method string, u *url.URL, owner string, r Request) (any, error, bool) {
	if !terminalPath(u.Path) {
		return nil, nil, false
	}
	if _, err := a.Control.Conversation(ctx, owner); err != nil {
		return nil, err, true
	}
	hub := a.Control.Terminals
	if u.Path == "/api/v1/terminals" {
		if method == "GET" {
			return map[string]any{"boot_id": a.BootID, "sessions": hub.List(owner)}, nil, true
		}
		var request terminal.Create
		if err := decode(r.Body, &request); err != nil {
			return nil, err, true
		}
		cwd, err := a.Control.ExecutionWorkspace(owner)
		if err != nil {
			return nil, err, true
		}
		info, err := hub.Create(owner, cwd, request)
		if err != nil {
			return nil, err, true
		}
		return map[string]any{"id": info.ID, "boot_id": a.BootID}, nil, true
	}
	parts := strings.Split(strings.TrimPrefix(u.Path, "/api/v1/terminals/"), "/")
	if len(parts) == 2 && parts[1] == "close" && method == "POST" && core.Identifier(parts[0]) {
		closectx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := hub.CloseSession(closectx, owner, parts[0]); err != nil {
			return nil, err, true
		}
		return map[string]bool{"closed": true}, nil, true
	}
	return nil, errors.New("invalid terminal operation"), true
}
func (a *Application) terminalSocket(w http.ResponseWriter, r *http.Request) bool {
	if !terminalPath(r.URL.Path) {
		return false
	}
	if len(r.Header.Values("Origin")) > 0 {
		writeError(w, http.StatusForbidden, protocol.APIError{Code: "terminal_origin", Message: "use the native owner terminal bridge"})
		return true
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/terminals/"), "/")
	if len(parts) != 2 || parts[1] != "socket" {
		return false
	}
	owner, err := a.resolveOwner(r.Context(), r.URL.Query().Get("assistant_id"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return true
	}
	if !core.Identifier(owner) || r.Method != "GET" {
		http.Error(w, "invalid terminal scope", 400)
		return true
	}
	if _, err := a.Control.Conversation(r.Context(), owner); err != nil {
		http.Error(w, "unknown terminal owner", 404)
		return true
	}
	s, err := a.Control.Terminals.Get(owner, parts[0])
	if err != nil {
		http.Error(w, err.Error(), 404)
		return true
	}
	history, output, detach, err := s.Attach()
	if err != nil {
		http.Error(w, err.Error(), 409)
		return true
	}
	defer detach()
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return true
	}
	defer conn.CloseNow()
	conn.SetReadLimit(16384)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	write := func(kind websocket.MessageType, data []byte) error {
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		return conn.Write(ctx, kind, data)
	}
	if err = write(websocket.MessageBinary, history); err != nil {
		return true
	}
	go func() {
		defer cancel()
		for {
			kind, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			if kind == websocket.MessageBinary {
				err = s.Write(data)
			} else {
				var size terminal.Size
				err = core.DecodeJSON(data, &size)
				if err == nil {
					err = s.Resize(size)
				}
			}
			if err != nil {
				conn.Close(websocket.StatusPolicyViolation, "invalid terminal input or closed terminal")
				return
			}
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return true
		case <-output:
			data, ok := s.Drain()
			if !ok {
				conn.Close(websocket.StatusTryAgainLater, "terminal client fell behind; reconnect for recent output")
				return true
			}
			if write(websocket.MessageBinary, data) != nil {
				return true
			}
		case <-s.Done():
			data, ok := s.Drain()
			if !ok {
				return true
			}
			if len(data) > 0 && write(websocket.MessageBinary, data) != nil {
				return true
			}
			body, _ := json.Marshal(map[string]any{"exit_code": s.Info().ExitCode})
			write(websocket.MessageText, body)
			conn.Close(websocket.StatusNormalClosure, "")
			return true
		}
	}
}
