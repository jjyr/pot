package webui

import (
	"errors"
	"github.com/coder/websocket"
	"net/http"
	"net/url"
	"strings"
)

func desktopAllowed(method, path string) bool {
	if path == "/api/v1/desktop/sessions" {
		return method == "POST"
	}
	if strings.HasPrefix(path, "/desktop-assets/") {
		return method == "GET" && strings.HasSuffix(path, ".js") && !strings.Contains(path, "..")
	}
	if !strings.HasPrefix(path, "/desktop/") {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(path, "/desktop/"), "/")
	if len(parts) < 2 || parts[0] == "" {
		return false
	}
	suffix := strings.Join(parts[1:], "/")
	if method == "GET" {
		switch suffix {
		case "", "rfb", "i18n.js", "locales.json", "api/status", "api/metrics":
			return true
		}
	}
	if method == "POST" {
		switch suffix {
		case "api/control", "api/browser", "api/close":
			return true
		}
	}
	return false
}
func (s *Server) proxyDesktopSocket(w http.ResponseWriter, r *http.Request, generation uint64) {
	ctx, finish, err := s.stream(r, generation)
	if err != nil {
		failure(w, 401, "browser session unavailable")
		return
	}
	defer finish()

	u, err := url.Parse(s.upstream.URL + r.URL.RequestURI())
	if err != nil {
		failure(w, 400, "invalid desktop route")
		return
	}
	if u.Scheme == "https" {
		u.Scheme = "wss"
	} else {
		u.Scheme = "ws"
	}
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+s.upstream.Credential)
	headers.Set("X-Pot-Browser-ID", sessionOwner(r))
	if s.upstream.ControlID != "" {
		headers.Set("X-Pot-Control-ID", s.upstream.ControlID)
	}
	upstream, _, err := websocket.Dial(ctx, u.String(), &websocket.DialOptions{HTTPClient: s.upstream.Client, HTTPHeader: headers, Subprotocols: []string{"binary"}})
	if err != nil {
		failure(w, 502, "desktop unavailable")
		return
	}
	defer upstream.CloseNow()
	upstream.SetReadLimit(16 * 1024 * 1024)
	client, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"binary"}})
	if err != nil {
		return
	}
	defer client.CloseNow()
	client.SetReadLimit(1024 * 1024)
	done := make(chan error, 2)
	copySocket := func(destination, source *websocket.Conn) {
		for {
			kind, data, err := source.Read(ctx)
			if err != nil {
				done <- err
				return
			}
			if kind != websocket.MessageBinary {
				done <- errors.New("desktop protocol requires binary packets")
				return
			}
			if err = destination.Write(ctx, kind, data); err != nil {
				done <- err
				return
			}
		}
	}
	go copySocket(client, upstream)
	go copySocket(upstream, client)
	select {
	case <-ctx.Done():
	case <-done:
	}
	finish()
	upstream.CloseNow()
	client.CloseNow()
}
