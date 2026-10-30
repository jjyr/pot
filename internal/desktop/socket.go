//go:build darwin || (linux && desktop)

package desktop

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/jjyr/pot/internal/localui"
)

// transportBootstrap carries the loopback WebSocket endpoint and bearer
// token used by renderer processes to reach the desktop caller.
type transportBootstrap struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}

// desktopSocket is a loopback-only HTTP server that upgrades verified
// requests into the localui WebSocket protocol.
type desktopSocket struct {
	bootstrap transportBootstrap
	server    *http.Server
	cancel    context.CancelFunc
	once      sync.Once
}

// startDesktopSocket binds an ephemeral loopback listener, generates a
// process-local token, and serves exactly one WebSocket endpoint at path.
func startDesktopSocket(ctx context.Context, path string, caller localui.Caller, controlID string) (*desktopSocket, error) {
	if !strings.HasPrefix(path, "/") {
		return nil, fmt.Errorf("desktop: socket path must be absolute: %q", path)
	}
	if caller == nil {
		return nil, errors.New("desktop: socket caller is nil")
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("desktop: bind loopback socket: %w", err)
	}

	token := rand.Text()

	sock := &desktopSocket{
		bootstrap: transportBootstrap{
			URL:   "ws://" + ln.Addr().String() + path,
			Token: token,
		},
	}

	connCtx, cancel := context.WithCancel(ctx)
	sock.cancel = cancel

	host := ln.Addr().String()
	originWails := "wails://localhost"
	expectedProto := "pot.v1"

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Path != path || r.URL.RawQuery != "" || r.URL.RawPath != "" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if r.Host != host {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		origin := r.Header.Get("Origin")
		// "null" Origin is accepted here only because this endpoint requires
		// the bearer token in Sec-WebSocket-Protocol. WebKit reports a null
		// Origin for opaque schemes such as the desktop renderer's wails://,
		// so rejecting it would break the authenticated bootstrap path.
		if origin != originWails && origin != "null" {
			http.Error(w, "forbidden origin", http.StatusForbidden)
			return
		}

		offered := splitProtocols(r.Header.Values("Sec-WebSocket-Protocol"))
		if !containsConst(offered, expectedProto) {
			http.Error(w, "missing protocol", http.StatusBadRequest)
			return
		}
		var tokenOK bool
		for _, p := range offered {
			if strings.HasPrefix(p, "pot-token.") {
				secret := strings.TrimPrefix(p, "pot-token.")
				if subtle.ConstantTimeCompare([]byte(secret), []byte(token)) == 1 {
					tokenOK = true
				}
			}
		}
		if !tokenOK {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}

		localui.ServeWebSocket(w, r, localui.SocketOptions{
			Caller:             caller,
			ExpectedControlID:  controlID,
			InsecureSkipVerify: true,
		})
	})

	sock.server = &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		MaxHeaderBytes:    16 * 1024,
		BaseContext: func(net.Listener) context.Context {
			return connCtx
		},
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		if serveErr := sock.server.Serve(ln); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			sock.cancel()
		}
	}()

	go func() {
		select {
		case <-ctx.Done():
			sock.Close()
		case <-done:
		}
	}()

	return sock, nil
}

// Close tears down the socket idempotently. Hijacked WebSocket connections
// observe the cancellation via the server base context.
func (s *desktopSocket) Close() error {
	if s == nil {
		return nil
	}
	var err error
	s.once.Do(func() {
		if s.cancel != nil {
			s.cancel()
		}
		if s.server != nil {
			err = s.server.Close()
			if errors.Is(err, http.ErrServerClosed) {
				err = nil
			}
		}
	})
	return err
}

func splitProtocols(values []string) []string {
	var out []string
	for _, v := range values {
		for _, p := range strings.Split(v, ",") {
			p = strings.TrimSpace(p)
			if p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

func containsConst(values []string, want string) bool {
	for _, v := range values {
		if subtle.ConstantTimeCompare([]byte(v), []byte(want)) == 1 {
			return true
		}
	}
	return false
}

// desktopCaller adapts Service methods into the localui.Caller interface
// exposed to the renderer over the socket.
func desktopCaller(s *Service) localui.Caller {
	if s == nil {
		return nil
	}
	return &desktopCallerImpl{svc: s}
}

type desktopCallerImpl struct {
	svc *Service
}

func (c *desktopCallerImpl) Call(ctx context.Context, method, path string, body json.RawMessage) (json.RawMessage, error) {
	if err := validateAPIRequest(method, path); err != nil {
		return nil, err
	}
	u, err := url.ParseRequestURI(path)
	if err != nil || u.IsAbs() || u.Host != "" {
		return nil, fmt.Errorf("invalid application path")
	}
	space := u.Query().Get("space_id")
	if u.Path == "/api/v1/spaces" {
		if method == "GET" {
			data, err := json.Marshal(c.svc.spaces.Snapshot())
			if err != nil {
				return nil, err
			}
			return json.RawMessage(data), nil
		}
		return nil, fmt.Errorf("local UI scope is fixed; connect peers through Agent Link")
	}
	return c.svc.spaces.Call(ctx, space, method, path, body)
}

func (c *desktopCallerImpl) Stream(ctx context.Context, path string, receive func(localui.Event) error) error {
	scope, err := validateEventsPath(path)
	if err != nil {
		return err
	}
	return c.svc.spaces.Stream(ctx, scope, path, receive)
}

func parseLocalRequestURI(raw string) (*url.URL, error) {
	u, err := url.ParseRequestURI(raw)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "" || u.Opaque != "" || u.Host != "" || u.User != nil {
		return nil, fmt.Errorf("desktop: absolute uri not allowed: %q", raw)
	}
	if u.Fragment != "" {
		return nil, fmt.Errorf("desktop: invalid uri: %q", raw)
	}
	// Reject percent-encoded sequences that would break path parsing if
	// decoded: %2F (%2f) and %5C (%5c) introduce structural separators,
	// %25 (%25) risks double decoding, and %2E (%2e) hides dot segments.
	// Percent-encoded colons (%3A) are permitted for linked assistant IDs.
	escaped := strings.ToLower(u.EscapedPath())
	if strings.Contains(escaped, "%2f") ||
		strings.Contains(escaped, "%5c") ||
		strings.Contains(escaped, "%25") ||
		strings.Contains(escaped, "%2e") {
		return nil, fmt.Errorf("desktop: invalid path: %q", u.Path)
	}
	if u.Path == "" || !strings.HasPrefix(u.Path, "/") {
		return nil, fmt.Errorf("desktop: uri path must be absolute: %q", raw)
	}
	if strings.ContainsAny(u.Path, "\\") {
		return nil, fmt.Errorf("desktop: invalid path: %q", u.Path)
	}
	if strings.Contains(u.Path, "//") {
		return nil, fmt.Errorf("desktop: invalid path: %q", u.Path)
	}
	if hasDotSegment(u.Path) {
		return nil, fmt.Errorf("desktop: invalid path: %q", u.Path)
	}
	return u, nil
}

func validateAPIRequest(method, path string) error {
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodDelete:
	default:
		return fmt.Errorf("desktop: unsupported method %q", method)
	}
	u, err := parseLocalRequestURI(path)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(u.Path, "/api/v1/") {
		return fmt.Errorf("desktop: invalid api path %q", path)
	}
	switch u.Path {
	case "/api/v1/ws", "/api/v1/events", "/api/v1/changes", "/api/v1/lifecycle":
		return fmt.Errorf("desktop: reserved path not callable %q", u.Path)
	}
	return nil
}

func validateEventsPath(path string) (string, error) {
	u, err := parseLocalRequestURI(path)
	if err != nil {
		return "", err
	}
	switch u.Path {
	case "/api/v1/events", "/api/v1/changes":
	default:
		return "", fmt.Errorf("desktop: unsupported events path %q", u.Path)
	}
	scope := strings.TrimSpace(u.Query().Get("space_id"))
	return scope, nil
}

func hasDotSegment(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		if seg == "." || seg == ".." {
			return true
		}
	}
	return false
}
