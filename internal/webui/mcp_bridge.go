package webui

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/jjyr/pot/internal/mcppublic"
)

// mcpBridge retains standard HTTP/SSE semantics while using the existing private
// Control connection. Only this server can attest a verified browser session.
func (s *Server) mcpBridge(w http.ResponseWriter, r *http.Request) bool {
	if !mcppublic.Route(r.URL.Path) {
		return false
	}
	if r.URL.RawPath != "" || r.Host != s.origin.Host || r.Header.Get("Origin") != "" && r.Header.Get("Origin") != s.origin.String() {
		failure(w, http.StatusForbidden, "browser origin rejected")
		return true
	}
	if !mcppublic.Method(r.URL.Path, r.Method) {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return true
	}
	target, err := url.Parse(s.upstream.URL)
	if err != nil {
		failure(w, 503, "upstream unavailable")
		return true
	}
	owner, _ := s.MCPOwnerSession(r)
	proxy := &httputil.ReverseProxy{
		Rewrite: func(p *httputil.ProxyRequest) {
			p.SetURL(target)
			p.Out.Host = r.Host
			for name := range p.Out.Header {
				if strings.HasPrefix(strings.ToLower(name), "x-pot-") {
					p.Out.Header.Del(name)
				}
			}
			p.Out.Header.Del("Cookie")
			p.Out.Header.Set("X-Pot-Control-ID", s.upstream.ControlID)
			p.Out.Header.Set("X-Pot-MCP-Origin", s.origin.String())
			p.Out.Header.Set("X-Pot-MCP-Authorization", r.Header.Get("Authorization"))
			p.Out.Header.Set("X-Pot-MCP-Owner", owner)
			p.Out.Header.Set("Authorization", "Bearer "+s.upstream.Credential)
		},
		Transport:     s.upstream.Client.Transport,
		FlushInterval: -1,
		ErrorHandler:  func(w http.ResponseWriter, _ *http.Request, _ error) { failure(w, 502, "upstream unavailable") },
	}
	proxy.ServeHTTP(w, r)
	return true
}
