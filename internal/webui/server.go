// Package webui publishes the browser UI through a separate authenticated
// allowlist. Control credentials are server-held and never accepted from browsers.
package webui

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jjyr/pot/internal/instance"
)

type Config struct {
	Token      string `json:"token"`
	SessionKey string `json:"session_key"`
	Generation uint64 `json:"generation"`
}

func NewConfig() Config                 { return Config{rand.Text(), rand.Text(), 1} }
func (c Config) Save(path string) error { return instance.WriteJSON(path, c) }

type Upstream struct {
	URL        string
	Credential string
	Client     *http.Client
}
type Options struct {
	Origin   string
	Config   Config
	Upstream Upstream
	Assets   fs.FS
}
type Server struct {
	mu       sync.Mutex
	config   Config
	origin   *url.URL
	upstream Upstream
	assets   http.Handler
	streams  map[uint64]context.CancelFunc
	next     uint64
	failures map[string]attempt
}
type attempt struct {
	count int
	until time.Time
}

func New(o Options) (*Server, error) {
	origin, err := url.Parse(o.Origin)
	if err != nil || origin.Host == "" || (origin.Scheme != "http" && origin.Scheme != "https") || origin.User != nil || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" {
		return nil, errors.New("browser server requires an exact public origin")
	}
	if origin.Scheme == "http" {
		host := origin.Hostname()
		ip := net.ParseIP(host)
		if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return nil, errors.New("non-loopback browser server requires HTTPS")
		}
	}
	u, err := url.Parse(o.Upstream.URL)
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || o.Upstream.Credential == "" || o.Upstream.Client == nil {
		return nil, errors.New("explicit protected Control upstream required")
	}
	if len(o.Config.Token) < 20 || len(o.Config.SessionKey) < 20 || o.Config.Generation == 0 || o.Assets == nil {
		return nil, errors.New("invalid browser access configuration")
	}
	return &Server{config: o.Config, origin: origin, upstream: o.Upstream, assets: http.FileServer(http.FS(o.Assets)), streams: map[uint64]context.CancelFunc{}, failures: map[string]attempt{}}, nil
}

// ReplaceAccess invalidates active streams and every previously issued cookie.
func (s *Server) ReplaceAccess(c Config) error {
	if len(c.Token) < 20 || len(c.SessionKey) < 20 || c.Generation == 0 {
		return errors.New("invalid access configuration")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if c.Generation <= s.config.Generation {
		return errors.New("access generation must advance")
	}
	s.config = c
	for _, cancel := range s.streams {
		cancel()
	}
	s.streams = map[uint64]context.CancelFunc{}
	return nil
}
func (s *Server) cookie(c Config, expires int64) string {
	text := strconv.FormatUint(c.Generation, 10) + "." + strconv.FormatInt(expires, 10)
	mac := hmac.New(sha256.New, []byte(c.SessionKey))
	mac.Write([]byte(s.origin.String() + "\n" + text))
	return text + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func (s *Server) authenticated(r *http.Request, c Config) bool {
	cookie, err := r.Cookie("pot_session")
	if err != nil {
		return false
	}
	parts := strings.Split(cookie.Value, ".")
	if len(parts) != 3 {
		return false
	}
	expires, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || expires <= time.Now().Unix() || expires > time.Now().Add(31*24*time.Hour).Unix() {
		return false
	}
	return hmac.Equal([]byte(cookie.Value), []byte(s.cookie(c, expires)))
}
func (s *Server) sameOrigin(r *http.Request) bool {
	return r.Host == s.origin.Host && (r.Header.Get("Origin") == "" || r.Header.Get("Origin") == s.origin.String()) && r.Header.Get("Sec-Fetch-Site") != "cross-site"
}
func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func failure(w http.ResponseWriter, status int, message string) {
	reply(w, status, map[string]string{"error": message})
}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
	if !s.sameOrigin(r) {
		failure(w, 403, "browser origin rejected")
		return
	}
	s.mu.Lock()
	c := s.config
	s.mu.Unlock()
	auth := s.authenticated(r, c)
	switch r.URL.Path {
	case "/access/status":
		if r.Method != "GET" {
			failure(w, 405, "method not allowed")
			return
		}
		reply(w, 200, map[string]any{"authenticated": auth, "scope": "control", "push_available": false, "desktop_available": false})
		return
	case "/access/token":
		if r.Method != "POST" {
			failure(w, 405, "method not allowed")
			return
		}
		host, _, _ := net.SplitHostPort(r.RemoteAddr)
		s.mu.Lock()
		a := s.failures[host]
		if a.until.Before(time.Now()) {
			a = attempt{until: time.Now().Add(time.Minute)}
		}
		if a.count >= 10 {
			s.mu.Unlock()
			failure(w, 429, "too many token attempts")
			return
		}
		valid := subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+c.Token)) == 1
		if !valid {
			a.count++
			if len(s.failures) > 4096 {
				for key, v := range s.failures {
					if v.until.Before(time.Now()) {
						delete(s.failures, key)
					}
				}
			}
			s.failures[host] = a
		}
		s.mu.Unlock()
		if !valid {
			failure(w, 401, "access token rejected")
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "pot_session", Value: s.cookie(c, time.Now().Add(30*24*time.Hour).Unix()), Path: "/", Secure: s.origin.Scheme == "https", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 30 * 24 * 60 * 60})
		reply(w, 200, map[string]bool{"accepted": true})
		return
	case "/access/logout":
		if r.Method != "POST" {
			failure(w, 405, "method not allowed")
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "pot_session", Value: "", Path: "/", Secure: s.origin.Scheme == "https", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
		reply(w, 200, map[string]bool{"accepted": true})
		return
	}
	if strings.HasPrefix(r.URL.Path, "/access/") {
		failure(w, 404, "access route unavailable")
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		if !auth {
			failure(w, 401, "browser authentication required")
			return
		}
		if !Allowed(r.Method, r.URL.Path) {
			failure(w, 404, "browser route unavailable")
			return
		}
		s.proxy(w, r, c.Generation)
		return
	}
	if r.Method != "GET" && r.Method != "HEAD" {
		failure(w, 405, "method not allowed")
		return
	}
	if strings.HasSuffix(r.URL.Path, "/") && r.URL.Path != "/" {
		failure(w, 404, "asset unavailable")
		return
	}
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
	s.assets.ServeHTTP(w, r)
}
func Allowed(method, path string) bool {
	if strings.ContainsAny(path, "\\%") || strings.Contains(path, "//") || strings.Contains(path, "/../") {
		return false
	}
	if method == "GET" {
		switch path {
		case "/api/v1/runtime", "/api/v1/control", "/api/v1/spaces", "/api/v1/assistants", "/api/v1/providers", "/api/v1/agent", "/api/v1/config", "/api/v1/instructions", "/api/v1/snapshot", "/api/v1/events", "/api/v1/changes", "/api/v1/permissions", "/api/v1/usage/summary", "/api/v1/tasks", "/api/v1/schedules", "/api/v1/machines":
			return true
		}
	}
	if method == "POST" {
		switch path {
		case "/api/v1/assistants", "/api/v1/providers", "/api/v1/providers/default", "/api/v1/agent", "/api/v1/instructions", "/api/v1/messages", "/api/v1/turn/cancel", "/api/v1/permissions", "/api/v1/usage/policy", "/api/v1/usage/resume", "/api/v1/tasks", "/api/v1/schedules":
			return true
		}
		for _, prefix := range []string{"/api/v1/assistants/", "/api/v1/tasks/", "/api/v1/schedules/"} {
			if strings.HasPrefix(path, prefix) {
				id := strings.TrimPrefix(path, prefix)
				return id != "" && !strings.Contains(id, "/")
			}
		}
	}
	return false
}
func (s *Server) proxy(w http.ResponseWriter, r *http.Request, generation uint64) {
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	s.mu.Lock()
	if s.config.Generation != generation {
		s.mu.Unlock()
		failure(w, 401, "access changed")
		return
	}
	s.next++
	id := s.next
	s.streams[id] = cancel
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.streams, id); s.mu.Unlock() }()
	upstream, err := http.NewRequestWithContext(ctx, r.Method, s.upstream.URL+r.URL.RequestURI(), http.MaxBytesReader(w, r.Body, 16*1024*1024))
	if err != nil {
		failure(w, 400, "invalid upstream request")
		return
	}
	upstream.Header.Set("Authorization", "Bearer "+s.upstream.Credential)
	if value := r.Header.Get("Content-Type"); value != "" {
		upstream.Header.Set("Content-Type", value)
	}
	response, err := s.upstream.Client.Do(upstream)
	if err != nil {
		failure(w, 502, "Control unavailable")
		return
	}
	defer response.Body.Close()
	for _, key := range []string{"Content-Type", "Content-Disposition", "X-Accel-Buffering"} {
		if v := response.Header.Get(key); v != "" {
			w.Header().Set(key, v)
		}
	}
	w.WriteHeader(response.StatusCode)
	if strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		controller := http.NewResponseController(w)
		_ = controller.Flush()
		buf := make([]byte, 32768)
		for {
			n, err := response.Body.Read(buf)
			if n > 0 {
				_ = controller.SetWriteDeadline(time.Now().Add(15 * time.Second))
				if _, e := w.Write(buf[:n]); e != nil {
					return
				}
				if e := controller.Flush(); e != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}
	_, _ = io.Copy(w, io.LimitReader(response.Body, 32*1024*1024))
}
func (s *Server) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, cancel := range s.streams {
		cancel()
	}
	s.streams = map[uint64]context.CancelFunc{}
}
func (s *Server) String() string { return fmt.Sprintf("Browser server (%s)", s.origin) }
