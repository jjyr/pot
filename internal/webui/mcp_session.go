package webui

import "net/http"

// MCPOwnerSession supplies an opaque current browser-session binding to the MCP
// consent handler. It exports neither the login token nor owner API authority.
func (s *Server) MCPOwnerSession(r *http.Request) (string, bool) {
	if !s.sameOrigin(r) {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.authenticatedLocked(r, s.config) {
		return "", false
	}
	return sessionOwner(r), true
}
