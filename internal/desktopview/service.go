// Package desktopview is the owner-authenticated, ticket-scoped desktop gateway.
// Guest addresses and credentials never cross this boundary. Input authority is
// enforced on complete RFB packets independently of the browser's viewOnly flag.
package desktopview

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/jjyr/pot/internal/sandbox"
)

type Manager interface {
	PrepareDesktop(context.Context, string) (sandbox.DesktopBinding, error)
	ValidateDesktop(context.Context, sandbox.DesktopBinding) error
	OpenDesktop(context.Context, sandbox.DesktopBinding) (io.ReadWriteCloser, error)
	DesktopMetrics(context.Context, sandbox.DesktopBinding) (json.RawMessage, error)
	DesktopBrowser(context.Context, sandbox.DesktopBinding) error
}
type Identity struct {
	Owner string
	Valid func() bool
}
type Options struct {
	Manager     Manager
	Authorize   func(*http.Request) (Identity, error)
	CheckOrigin func(*http.Request) bool
	Assets      fs.FS
}
type Service struct {
	mu      sync.Mutex
	opening sync.Mutex
	options Options
	tickets map[string]*viewer
	closed  bool
}
type viewer struct {
	id, owner           string
	valid               func() bool
	expires             time.Time
	binding             sandbox.DesktopBinding
	connecting, control bool
	revision            uint64
	stream              io.ReadWriteCloser
	socket              *websocket.Conn
	gate                rfbGate
	cancel              context.CancelFunc
}

func New(options Options) (*Service, error) {
	if options.Manager == nil || options.Authorize == nil || options.CheckOrigin == nil || options.Assets == nil {
		return nil, errors.New("desktop gateway requires Manager, login authorization, exact Origin policy and packaged assets")
	}
	return &Service{options: options, tickets: map[string]*viewer{}}, nil
}
func (v *viewer) live() bool { return time.Now().Before(v.expires) && v.valid != nil && v.valid() }
func (s *Service) revokeLocked(v *viewer) {
	delete(s.tickets, v.id)
	v.control = false
	v.revision++
	if v.cancel != nil {
		v.cancel()
	}
	if v.stream != nil {
		v.stream.Close()
	}
	if v.socket != nil {
		v.socket.CloseNow()
	}
}
func (s *Service) RevokeOwner(owner string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, v := range s.tickets {
		if v.owner == owner {
			s.revokeLocked(v)
		}
	}
}
func (s *Service) RevokeSandbox(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, v := range s.tickets {
		if v.binding.SandboxID == id {
			s.revokeLocked(v)
		}
	}
}
func (s *Service) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	for _, v := range s.tickets {
		s.revokeLocked(v)
	}
	return nil
}
func privateHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; connect-src 'self'; img-src 'self' data:; frame-ancestors 'self'")
}
func reply(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(value)
}
func fail(w http.ResponseWriter, status int, code string) {
	reply(w, status, map[string]any{"error": code})
}
func body(r *http.Request, out any) error {
	data, err := io.ReadAll(io.LimitReader(r.Body, 65537))
	if err != nil {
		return err
	}
	if len(data) > 65536 {
		return errors.New("body too large")
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}
func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	privateHeaders(w)
	identity, err := s.options.Authorize(r)
	if err != nil || identity.Owner == "" || identity.Valid == nil || !identity.Valid() {
		fail(w, 401, "login_required")
		return
	}
	if !s.options.CheckOrigin(r) {
		fail(w, 403, "origin_not_allowed")
		return
	}
	if r.URL.RawQuery != "" && r.URL.RawQuery != "native=1" {
		fail(w, 400, "desktop_query")
		return
	}
	if r.URL.Path == "/api/v1/desktop/sessions" && r.Method == http.MethodPost {
		s.open(w, r, identity)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/desktop-assets/") && r.Method == http.MethodGet {
		name := strings.TrimPrefix(r.URL.Path, "/desktop-assets/")
		if !fs.ValidPath(name) || strings.Contains(name, "\\") || !strings.HasSuffix(name, ".js") {
			fail(w, 404, "desktop_asset")
			return
		}
		s.asset(w, r, "desktop-assets/"+name, "text/javascript; charset=utf-8")
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if len(parts) < 3 || parts[0] != "desktop" {
		fail(w, 404, "desktop_route")
		return
	}
	id, suffix := parts[1], strings.Join(parts[2:], "/")
	s.mu.Lock()
	v := s.tickets[id]
	valid := !s.closed && v != nil && v.owner == identity.Owner && v.live()
	s.mu.Unlock()
	if !valid {
		fail(w, 403, "desktop_session")
		return
	}
	if r.Method == http.MethodGet {
		switch suffix {
		case "":
			s.asset(w, r, "desktop/index.html", "text/html; charset=utf-8")
		case "i18n.js":
			s.asset(w, r, "desktop/i18n.js", "text/javascript; charset=utf-8")
		case "locales.json":
			s.asset(w, r, "desktop/locales.json", "application/json; charset=utf-8")
		case "api/status":
			s.mu.Lock()
			owner := "none"
			if v.control {
				owner = "human"
			}
			result := map[string]any{"owner": owner, "epoch": v.revision, "capabilities": v.binding.Capabilities, "sandbox_id": v.binding.SandboxID}
			s.mu.Unlock()
			reply(w, 200, result)
		case "api/metrics":
			s.metrics(w, r, v)
		case "rfb":
			s.connect(w, r, v, identity)
		default:
			fail(w, 404, "desktop_route")
		}
		return
	}
	if r.Method == http.MethodPost {
		switch suffix {
		case "api/close":
			s.mu.Lock()
			if s.tickets[v.id] == v {
				s.revokeLocked(v)
			}
			s.mu.Unlock()
			reply(w, 200, map[string]bool{"closed": true})
		case "api/control":
			s.control(w, r, v)
		case "api/browser":
			s.mu.Lock()
			defer s.mu.Unlock()
			if s.tickets[v.id] != v || !v.live() || !v.control || !v.binding.Capabilities.Browser {
				fail(w, 409, "take_over_required")
				return
			}
			if s.options.Manager.DesktopBrowser(r.Context(), v.binding) != nil {
				fail(w, 503, "desktop_browser")
				return
			}
			reply(w, 200, map[string]bool{"opened": true})
		default:
			fail(w, 404, "desktop_route")
		}
		return
	}
	fail(w, 405, "desktop_method")
}
func (s *Service) asset(w http.ResponseWriter, r *http.Request, name, kind string) {
	if !fs.ValidPath(name) || path.Clean(name) != name {
		fail(w, 404, "desktop_asset")
		return
	}
	data, err := fs.ReadFile(s.options.Assets, name)
	if err != nil {
		fail(w, 404, "desktop_asset")
		return
	}
	w.Header().Set("Content-Type", kind)
	w.Write(data)
}
func (s *Service) open(w http.ResponseWriter, r *http.Request, identity Identity) {
	s.opening.Lock()
	defer s.opening.Unlock()
	var args struct {
		SandboxID string `json:"sandbox_id"`
	}
	if body(r, &args) != nil || args.SandboxID == "" {
		fail(w, 400, "sandbox_id_required")
		return
	}
	s.mu.Lock()
	count := 0
	for _, v := range s.tickets {
		if !v.live() {
			s.revokeLocked(v)
		} else if v.owner == identity.Owner {
			count++
		}
	}
	limited := s.closed || len(s.tickets) >= 64 || count >= 16
	s.mu.Unlock()
	if limited {
		fail(w, 429, "desktop_limit")
		return
	}
	binding, err := s.options.Manager.PrepareDesktop(r.Context(), args.SandboxID)
	if err != nil {
		fail(w, 503, "desktop_unavailable")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || !identity.Valid() {
		fail(w, 401, "session_expired")
		return
	}
	id := rand.Text()
	v := &viewer{id: id, owner: identity.Owner, valid: identity.Valid, expires: time.Now().Add(time.Hour), binding: binding}
	s.tickets[id] = v
	reply(w, 200, map[string]any{"session": map[string]any{"id": id, "sandbox_id": binding.SandboxID, "capabilities": binding.Capabilities, "expires_in_seconds": 3600}, "path": "/desktop/" + id + "/"})
}
func (s *Service) control(w http.ResponseWriter, r *http.Request, v *viewer) {
	var args struct {
		Action string `json:"action"`
	}
	if body(r, &args) != nil {
		fail(w, 400, "desktop_action")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tickets[v.id] != v || !v.live() {
		fail(w, 403, "desktop_session")
		return
	}
	switch args.Action {
	case "take":
		if v.stream == nil || !v.binding.Capabilities.Input {
			fail(w, 409, "desktop_not_connected")
			return
		}
		for _, other := range s.tickets {
			if other != v && other.binding.SandboxID == v.binding.SandboxID && other.control && other.stream != nil {
				fail(w, 409, "desktop_already_controlled")
				return
			}
		}
		if v.gate.changed() != nil {
			s.revokeLocked(v)
			fail(w, 409, "desktop_reconnect_required")
			return
		}
		v.control = true
		v.revision++
	case "return":
		v.control = false
		v.revision++
		if v.stream != nil {
			if err := writeBounded(v.stream, v.gate.release()); err != nil {
				s.revokeLocked(v)
				fail(w, 503, "desktop_release_unconfirmed")
				return
			}
			if v.gate.changed() != nil {
				s.revokeLocked(v)
			}
		}
	case "close":
		s.revokeLocked(v)
	default:
		fail(w, 400, "desktop_action")
		return
	}
	reply(w, 200, map[string]bool{"ok": true})
}
func (s *Service) metrics(w http.ResponseWriter, r *http.Request, v *viewer) {
	raw, err := s.options.Manager.DesktopMetrics(r.Context(), v.binding)
	if err != nil {
		fail(w, 503, "desktop_metrics")
		return
	}
	var fields map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if decoder.Decode(&fields) != nil {
		fail(w, 503, "desktop_metrics")
		return
	}
	safe := map[string]any{"sandbox_id": v.binding.SandboxID}
	for _, key := range []string{"image", "image_bytes", "cpu_percent", "cpus", "memory_used", "memory_total", "memory_gib", "disk_used", "disk_total", "workspace_bytes", "uptime_seconds"} {
		value := fields[key]
		switch value := value.(type) {
		case json.Number:
			safe[key] = value
		case string:
			if key == "image" && len(value) <= 256 {
				safe[key] = value
			}
		}
	}
	reply(w, 200, safe)
}
func writeBounded(stream io.ReadWriteCloser, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	if conn, ok := stream.(net.Conn); ok {
		conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		defer conn.SetWriteDeadline(time.Time{})
		_, err := io.Copy(conn, strings.NewReader(string(data)))
		return err
	}
	done := make(chan error, 1)
	go func() { _, err := io.Copy(stream, strings.NewReader(string(data))); done <- err }()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-timer.C:
		stream.Close()
		return errors.New("desktop write deadline")
	}
}
func (s *Service) connect(w http.ResponseWriter, r *http.Request, v *viewer, identity Identity) {
	s.mu.Lock()
	if s.tickets[v.id] != v || !v.live() || v.stream != nil || v.connecting {
		s.mu.Unlock()
		fail(w, 409, "desktop_connected")
		return
	}
	v.connecting = true
	s.mu.Unlock()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	stream, err := s.options.Manager.OpenDesktop(ctx, v.binding)
	if err != nil {
		s.mu.Lock()
		v.connecting = false
		s.mu.Unlock()
		fail(w, 503, "desktop_transport")
		return
	}
	defer stream.Close()
	socket, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"binary"}, InsecureSkipVerify: true})
	if err != nil {
		s.mu.Lock()
		v.connecting = false
		s.mu.Unlock()
		return
	}
	defer socket.CloseNow()
	socket.SetReadLimit(rfbLimit)
	s.mu.Lock()
	if s.tickets[v.id] != v || !v.live() || !identity.Valid() {
		v.connecting = false
		s.mu.Unlock()
		return
	}
	v.stream, v.socket, v.cancel = stream, socket, cancel
	v.gate = rfbGate{}
	v.connecting = false
	v.control = false
	v.revision++
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if v.stream == stream {
			writeBounded(stream, v.gate.release())
			v.stream = nil
			v.socket = nil
			v.cancel = nil
			v.control = false
			v.revision++
		}
	}()
	ended := make(chan struct{}, 2)
	go func() {
		defer func() { ended <- struct{}{} }()
		buf := make([]byte, 65536)
		for {
			n, err := stream.Read(buf)
			if n > 0 {
				sendCtx, stop := context.WithTimeout(ctx, 5*time.Second)
				sendErr := socket.Write(sendCtx, websocket.MessageBinary, buf[:n])
				stop()
				if sendErr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	go func() {
		defer func() { ended <- struct{}{} }()
		for {
			kind, data, err := socket.Read(ctx)
			if err != nil || kind != websocket.MessageBinary {
				return
			}
			s.mu.Lock()
			if s.tickets[v.id] != v || v.stream != stream || !v.live() || !identity.Valid() {
				s.mu.Unlock()
				return
			}
			out, err := v.gate.push(data, v.control, v.binding.Capabilities.Clipboard)
			if err == nil {
				err = writeBounded(stream, out)
			}
			s.mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ended:
			return
		case <-ticker.C:
			s.mu.Lock()
			live := s.tickets[v.id] == v && v.live() && identity.Valid()
			s.mu.Unlock()
			if !live || s.options.Manager.ValidateDesktop(ctx, v.binding) != nil {
				return
			}
		}
	}
}
