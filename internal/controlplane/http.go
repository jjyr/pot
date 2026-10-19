package controlplane

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

// Server keeps owner HTTP separate from authenticated peer TCP services.
type Server struct {
	Directory    *Directory
	Handler      Handler
	OwnerHandler http.Handler
	OnEnroll     func(context.Context, Machine) error
	OnPresence   func(string, bool)
}

func bearer(r *http.Request) string {
	v := r.Header.Get("Authorization")
	if !strings.HasPrefix(v, "Bearer ") {
		return ""
	}
	return strings.TrimPrefix(v, "Bearer ")
}
func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(value)
}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.Directory == nil || s.Directory.options.Embedded || s.Directory.Role() != "control" {
		http.Error(w, "standalone Control required", http.StatusForbidden)
		return
	}
	if r.Header.Get("Origin") != "" {
		http.Error(w, "browser Origin rejected", http.StatusForbidden)
		return
	}
	if r.URL.Path == "/control/v1/identity" && r.Method == http.MethodGet {
		writeJSON(w, s.Directory.Identity())
		return
	}
	if s.OwnerHandler != nil && strings.HasPrefix(r.URL.Path, "/control/v1/owner/") && s.Directory.AuthenticateOwner(bearer(r)) {
		http.StripPrefix("/control/v1/owner", s.OwnerHandler).ServeHTTP(w, r)
		return
	}
	http.Error(w, "Control authentication rejected", http.StatusForbidden)
}
