package preview

import (
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/jjyr/pot/internal/localui"
)

// Handler is a loopback-only developer fixture. Production browser auth and
// Control routes are deliberately not implemented by this preview transport.
func Handler(service *Service, assets fs.FS) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/preview/ws", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") == "" {
			http.Error(w, "Preview requires the same origin", http.StatusForbidden)
			return
		}
		localui.ServeWebSocket(w, r, localui.SocketOptions{Caller: SocketCaller(service)})
	})
	mux.Handle("GET /", http.FileServerFS(assets))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		host := r.Host
		if name, _, err := net.SplitHostPort(host); err == nil {
			host = name
		}
		ip := net.ParseIP(host)
		if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			http.Error(w, "Preview requires a loopback Host", http.StatusForbidden)
			return
		}
		origin := r.Header.Get("Origin")
		if origin != "" {
			parsed, err := url.Parse(origin)
			if err != nil || parsed.Host != r.Host || parsed.Scheme != "http" {
				http.Error(w, "Preview requires the same origin", http.StatusForbidden)
				return
			}
		}
		if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			http.Error(w, "Cross-site preview access denied", http.StatusForbidden)
			return
		}
		if (r.URL.Path == "/api/preview" || strings.HasPrefix(r.URL.Path, "/api/preview/")) && r.URL.Path != "/api/preview/ws" {
			http.NotFound(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	})
}
