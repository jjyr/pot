package webui

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/jjyr/pot/internal/localui"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

const maxSocketResponseBytes = 16 << 20

func isForbiddenSocketCall(path string) bool {
	switch path {
	case "/api/v1/events", "/api/v1/changes", "/api/v1/lifecycle", "/api/v1/ws":
		return true
	}
	return strings.HasPrefix(path, "/api/v1/events/") ||
		strings.HasPrefix(path, "/api/v1/changes/") ||
		strings.HasPrefix(path, "/api/v1/lifecycle/") ||
		strings.HasPrefix(path, "/api/v1/ws/")
}

func isSubscriptionPath(path string) bool {
	return path == "/api/v1/events" || path == "/api/v1/changes"
}

func isMailAPI(path string) bool { return path == protocol.AssistantMailAPI }

func isOAuthRoute(method, path string) bool {
	if method != http.MethodGet && method != http.MethodPost {
		return false
	}
	return path == "/oauth/consent" || path == "/oauth/connections"
}

func canonicalSocketPath(path string) bool {
	if path == "" || path[0] != '/' {
		return false
	}
	if strings.ContainsAny(path, "\\%#") || strings.Contains(path, "//") {
		return false
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

func parseSocketURL(raw string) (*url.URL, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil ||
		u.Fragment != "" || u.Opaque != "" {
		return nil, false
	}
	escaped := strings.ToLower(u.EscapedPath())
	if strings.Contains(escaped, "%2f") || strings.Contains(escaped, "%5c") ||
		strings.Contains(escaped, "%25") || strings.Contains(escaped, "%2e") {
		return nil, false
	}
	if !canonicalSocketPath(u.Path) {
		return nil, false
	}
	return u, true
}

func browserHTTPAllowed(method, path string) bool {
	if method != http.MethodGet {
		return false
	}
	if strings.HasPrefix(path, "/api/v1/resources/") {
		rest := strings.TrimPrefix(path, "/api/v1/resources/")
		parts := strings.Split(rest, "/")
		if len(parts) != 2 || !core.Identifier(parts[0]) {
			return false
		}
		switch parts[1] {
		case "content", "download", "render", "render-document":
			return Allowed(method, path)
		}
		return false
	}
	if strings.HasPrefix(path, "/api/v1/message-files/") {
		rest := strings.TrimPrefix(path, "/api/v1/message-files/")
		return core.Identifier(rest) && Allowed(method, path)
	}
	if strings.HasPrefix(path, "/api/v1/tasks/") {
		rest := strings.TrimPrefix(path, "/api/v1/tasks/")
		parts := strings.Split(rest, "/")
		if len(parts) != 2 || parts[1] != "file-download" || !core.Identifier(parts[0]) {
			return false
		}
		return Allowed(method, path)
	}
	return false
}

type socketCaller struct {
	server       *Server
	request      *http.Request
	config       Config
	sessionID    string
	originHost   string
	client       *http.Client
	streamClient *http.Client
}

func (c *socketCaller) Call(ctx context.Context, method, path string, body json.RawMessage) (json.RawMessage, error) {
	u, ok := parseSocketURL(path)
	if !ok {
		return nil, &localui.SocketError{Status: http.StatusForbidden}
	}
	if !c.server.authenticated(c.request, c.config) {
		return nil, &localui.SocketError{Status: http.StatusUnauthorized}
	}

	if isOAuthRoute(method, u.Path) {
		return c.callOAuth(ctx, method, u.RequestURI(), body)
	}

	if strings.HasPrefix(u.Path, "/api/v1/notifications/") {
		return c.serveSocketNotifications(ctx, method, u, body)
	}

	if isForbiddenSocketCall(u.Path) {
		return nil, &localui.SocketError{Status: http.StatusNotFound}
	}
	if !strings.HasPrefix(u.Path, "/api/") || !Allowed(method, u.Path) {
		return nil, &localui.SocketError{Status: http.StatusNotFound}
	}

	if isMailAPI(u.Path) {
		var relay protocol.RelayRequest
		if len(body) == 0 || protocol.DecodeStrict(body, &relay) != nil || relay.Operation != "history" {
			return nil, &localui.SocketError{Status: http.StatusForbidden}
		}
	}

	return c.callOrdinary(ctx, method, u.RequestURI(), body)
}

func (c *socketCaller) callOrdinary(ctx context.Context, method, target string, body json.RawMessage) (json.RawMessage, error) {
	var reader io.Reader
	if len(body) > 0 {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.server.upstream.URL+target, reader)
	if err != nil {
		return nil, &localui.SocketError{Status: http.StatusBadRequest}
	}
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+c.server.upstream.Credential)
	if c.server.upstream.ControlID != "" {
		req.Header.Set("X-Pot-Control-ID", c.server.upstream.ControlID)
	}
	req.Header.Set("X-Pot-Browser-ID", c.sessionID)
	return c.doBounded(req)
}

func (c *socketCaller) callOAuth(ctx context.Context, method, target string, body json.RawMessage) (json.RawMessage, error) {
	var reader io.Reader
	if len(body) > 0 {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.server.upstream.URL+target, reader)
	if err != nil {
		return nil, &localui.SocketError{Status: http.StatusBadRequest}
	}
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+c.server.upstream.Credential)
	if c.server.upstream.ControlID != "" {
		req.Header.Set("X-Pot-Control-ID", c.server.upstream.ControlID)
	}
	req.Header.Set("X-Pot-MCP-Origin", c.server.origin.String())
	req.Header.Set("X-Pot-MCP-Owner", c.sessionID)
	req.Host = c.originHost
	return c.doBounded(req)
}

func (c *socketCaller) serveSocketNotifications(ctx context.Context, method string, u *url.URL, body json.RawMessage) (json.RawMessage, error) {
	req := c.request.Clone(ctx)
	req.Method = method
	req.URL = &url.URL{Path: u.Path, RawQuery: u.RawQuery}
	req.RequestURI = ""
	req.Host = c.originHost
	if req.Header == nil {
		req.Header = make(http.Header)
	}
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	w := newSocketWriter(maxSocketResponseBytes)
	c.server.notificationRoute(w, req, c.config)
	if w.overflowed {
		return nil, &localui.SocketError{Status: http.StatusBadGateway}
	}
	status := w.status
	if status == 0 {
		status = http.StatusOK
	}
	if status >= 200 && status < 300 {
		return socketJSON(w.body.Bytes())
	}
	return nil, &localui.SocketError{Status: status, APIError: decodeSocketAPIError(w.body.Bytes())}
}

func (c *socketCaller) doBounded(req *http.Request) (json.RawMessage, error) {
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, &localui.SocketError{Status: http.StatusBadGateway}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSocketResponseBytes+1))
	if err != nil || len(body) > maxSocketResponseBytes {
		return nil, &localui.SocketError{Status: http.StatusBadGateway}
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return socketJSON(body)
	}
	return nil, &localui.SocketError{Status: resp.StatusCode, APIError: decodeSocketAPIError(body)}
}

func decodeSocketAPIError(body []byte) protocol.APIError {
	var apiErr protocol.APIError
	if json.Unmarshal(body, &apiErr) == nil && (apiErr.Code != "" || apiErr.Message != "") {
		return apiErr
	}
	var generic struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &generic) == nil && generic.Error != "" {
		return protocol.APIError{Message: generic.Error}
	}
	return protocol.APIError{}
}

func (c *socketCaller) Stream(ctx context.Context, path string, receive func(localui.Event) error) error {
	u, ok := parseSocketURL(path)
	if !ok || !isSubscriptionPath(u.Path) {
		return &localui.SocketError{Status: http.StatusForbidden}
	}
	if !c.server.authenticated(c.request, c.config) {
		return &localui.SocketError{Status: http.StatusUnauthorized}
	}

	caller := localui.NewHTTPClient(c.server.upstream.URL, c.streamClient, c.server.upstream.Credential, c.server.upstream.ControlID)
	return caller.Stream(ctx, u.RequestURI(), func(ev localui.Event) error {
		if !c.server.authenticated(c.request, c.config) {
			return &localui.SocketError{Status: http.StatusUnauthorized}
		}
		return receive(ev)
	})
}

type socketWriter struct {
	header     http.Header
	status     int
	body       bytes.Buffer
	limit      int
	overflowed bool
}

func newSocketWriter(limit int) *socketWriter {
	return &socketWriter{header: make(http.Header), limit: limit}
}

func (w *socketWriter) Header() http.Header { return w.header }

func (w *socketWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
}

func (w *socketWriter) Write(p []byte) (int, error) {
	if w.overflowed {
		return 0, io.ErrShortWrite
	}
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if w.body.Len()+len(p) > w.limit {
		w.overflowed = true
		return 0, io.ErrShortWrite
	}
	return w.body.Write(p)
}

func socketJSON(body []byte) (json.RawMessage, error) {
	if len(bytes.TrimSpace(body)) == 0 {
		return json.RawMessage("null"), nil
	}
	if !json.Valid(body) {
		return nil, &localui.SocketError{Status: http.StatusBadGateway}
	}
	return json.RawMessage(body), nil
}

type browserIDRoundTripper struct {
	base      http.RoundTripper
	sessionID string
}

func (rt *browserIDRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	if clone.Header == nil {
		clone.Header = make(http.Header)
	}
	clone.Header.Set("X-Pot-Browser-ID", rt.sessionID)
	return rt.base.RoundTrip(clone)
}

func noRedirect(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}

func (s *Server) socket(w http.ResponseWriter, r *http.Request, c Config) {
	if r.Method != http.MethodGet {
		failure(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.origin == nil || r.Header.Get("Origin") != s.origin.String() {
		failure(w, http.StatusForbidden, "browser origin rejected")
		return
	}
	if !s.authenticated(r, c) {
		failure(w, http.StatusUnauthorized, "browser authentication required")
		return
	}

	ctx, finish, err := s.stream(r, c.Generation)
	if err != nil {
		failure(w, http.StatusUnauthorized, "browser session unavailable")
		return
	}
	defer finish()

	sessionID := sessionOwner(r)

	client := *s.upstream.Client
	client.CheckRedirect = noRedirect
	client.Jar = nil

	baseTransport := s.upstream.Client.Transport
	if baseTransport == nil {
		baseTransport = http.DefaultTransport
	}
	streamClient := &http.Client{
		Transport:     &browserIDRoundTripper{base: baseTransport, sessionID: sessionID},
		CheckRedirect: noRedirect,
		Timeout:       0,
	}

	caller := &socketCaller{
		server:       s,
		request:      r,
		config:       c,
		sessionID:    sessionID,
		originHost:   r.Host,
		client:       &client,
		streamClient: streamClient,
	}

	localui.ServeWebSocket(w, r.WithContext(ctx), localui.SocketOptions{Caller: caller, ExpectedControlID: s.upstream.ControlID})
}
