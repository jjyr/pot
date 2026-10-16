package transport

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
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
		if desktopPath(r.URL.Path) {
			if app.Desktop == nil {
				writeError(w, 503, protocol.APIError{Code: "desktop_unavailable", Message: "Desktop gateway unavailable"})
				return
			}
			query := r.URL.Query()
			query.Del("assistant_id")
			query.Del("space_id")
			r.URL.RawQuery = query.Encode()
			app.Desktop.ServeHTTP(w, r)
			return
		}
		if app.terminalSocket(w, r) {
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/events" {
			streamSnapshots(w, r, app)
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/changes" {
			streamChanges(w, r, app)
			return
		}
		if app.download(w, r) {
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
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
func streamSnapshots(w http.ResponseWriter, r *http.Request, app *Application) {
	owner := r.URL.Query().Get("assistant_id")
	if owner == "" {
		owner = "main"
	}
	c, err := app.Control.Conversation(r.Context(), owner)
	if err != nil {
		writeError(w, http.StatusNotFound, protocol.APIError{Code: "owner_unavailable", Message: err.Error()})
		return
	}
	updates, remove, err := c.Subscribe(r.Context())
	if err != nil {
		writeError(w, http.StatusConflict, protocol.APIError{Code: "subscription_unavailable", Message: err.Error()})
		return
	}
	defer remove()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	controller := http.NewResponseController(w)
	if controller.Flush() != nil {
		return
	}
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			if writeHeartbeat(controller, w) != nil {
				return
			}
		case snapshot, ok := <-updates:
			if !ok {
				return
			}
			data, err := json.Marshal(snapshot)
			if err != nil {
				return
			}
			id := app.BootID + ":" + owner + ":" + strconv.FormatInt(snapshot.Cursor, 10)
			if err = writeSSE(controller, w, "snapshot", id, data); err != nil {
				return
			}
		}
	}
}
func writeHeartbeat(controller *http.ResponseController, w http.ResponseWriter) error {
	_ = controller.SetWriteDeadline(time.Now().Add(15 * time.Second))
	if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
		return err
	}
	return controller.Flush()
}
func writeSSE(controller *http.ResponseController, w http.ResponseWriter, event, id string, data []byte) error {
	if strings.ContainsAny(event+id, "\r\n") {
		return errors.New("invalid SSE identity")
	}
	_ = controller.SetWriteDeadline(time.Now().Add(15 * time.Second))
	if _, err := fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n", id, event, data); err != nil {
		return err
	}
	return controller.Flush()
}
func streamChanges(w http.ResponseWriter, r *http.Request, app *Application) {
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	general := app.Control.Changes.Subscribe()
	permissions := app.Control.Permissions.Changes.Subscribe()
	defer general.Close()
	defer permissions.Close()
	merged := make(chan map[string]uint64, 2)
	listen := func(s *core.Subscription) {
		for {
			topics, err := s.Next(ctx)
			if err != nil {
				return
			}
			select {
			case <-ctx.Done():
				return
			case merged <- topics:
			}
		}
	}
	go listen(general)
	go listen(permissions)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	controller := http.NewResponseController(w)
	if controller.Flush() != nil {
		return
	}
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	seq := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeat.C:
			if writeHeartbeat(controller, w) != nil {
				return
			}
		case topics := <-merged:
			seq++
			body, _ := json.Marshal(topics)
			if err := writeSSE(controller, w, "changed", app.BootID+":"+strconv.Itoa(seq), body); err != nil {
				return
			}
		}
	}
}
