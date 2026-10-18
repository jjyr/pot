package preview

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"time"
)

// Handler is a loopback-only developer fixture. Production browser auth and
// Control routes are deliberately not implemented by this preview transport.
func Handler(service *Service, assets fs.FS) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/preview/snapshot", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, service.Snapshot())
	})
	mux.HandleFunc("POST /api/preview/messages", func(w http.ResponseWriter, r *http.Request) {
		var request SendRequest
		if !decode(w, r, &request) {
			return
		}
		receipt, err := service.Send(request)
		if err != nil {
			writeJSON(w, HTTPStatus(err), asError(err))
			return
		}
		writeJSON(w, http.StatusAccepted, receipt)
	})
	mux.HandleFunc("POST /api/preview/cancel", func(w http.ResponseWriter, r *http.Request) {
		var request CancelRequest
		if !decode(w, r, &request) {
			return
		}
		if err := service.Cancel(request); err != nil {
			writeJSON(w, HTTPStatus(err), asError(err))
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"accepted": true})
	})
	mux.HandleFunc("GET /api/preview/events", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := w.(http.Flusher); !ok {
			http.Error(w, "Streaming unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-Accel-Buffering", "no")
		controller := http.NewResponseController(w)
		updates, stop := service.Subscribe()
		defer stop()
		keepalive := time.NewTicker(5 * time.Second)
		defer keepalive.Stop()
		for {
			_ = controller.SetWriteDeadline(time.Now().Add(10 * time.Second))
			select {
			case <-r.Context().Done():
				return
			case <-service.ctx.Done():
				return
			case snapshot := <-updates:
				data, err := json.Marshal(snapshot)
				if err != nil {
					return
				}
				if _, err = fmt.Fprintf(w, "event: snapshot\ndata: %s\n\n", data); err != nil {
					return
				}
			case <-keepalive.C:
				if _, err := io.WriteString(w, ": keepalive\n\n"); err != nil {
					return
				}
			}
			if controller.Flush() != nil {
				return
			}
		}
	})
	mux.Handle("GET /", http.FileServerFS(assets))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
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
		mux.ServeHTTP(w, r)
	})
}

func decode(w http.ResponseWriter, r *http.Request, target any) bool {
	if r.Header.Get("Content-Type") != "application/json" {
		writeJSON(w, http.StatusUnsupportedMediaType, &Error{"invalid_request", "Expected application/json"})
		return false
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeJSON(w, http.StatusBadRequest, &Error{"invalid_request", "Invalid preview request"})
		return false
	}
	if decoder.Decode(new(any)) != io.EOF {
		writeJSON(w, http.StatusBadRequest, &Error{"invalid_request", "Expected a single request"})
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
