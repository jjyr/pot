//go:build darwin

package desktop

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/internal/desktopview"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"
)

type nativeBootstrap struct {
	path    string
	expires time.Time
}
type nativeDesktop struct {
	origin, secret, cookieName string
	viewer                     *desktopview.Service
	server                     *http.Server
	cancel                     context.CancelFunc
	mu                         sync.Mutex
	bootstrap                  map[string]nativeBootstrap
	once                       sync.Once
}

func startNativeDesktop(parent context.Context, manager desktopview.Manager, assets fs.FS) (*nativeDesktop, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	n := &nativeDesktop{origin: "http://" + listener.Addr().String(), secret: rand.Text(), cookieName: "pot-native-" + rand.Text(), bootstrap: map[string]nativeBootstrap{}, cancel: cancel}
	viewer, err := desktopview.New(desktopview.Options{Manager: manager, Assets: assets, CheckOrigin: func(r *http.Request) bool {
		return r.Host == listener.Addr().String() && (r.Header.Get("Origin") == "" || r.Header.Get("Origin") == n.origin)
	}, Authorize: func(r *http.Request) (desktopview.Identity, error) {
		cookie, err := r.Cookie(n.cookieName)
		if err != nil || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(n.secret)) != 1 {
			return desktopview.Identity{}, errors.New("native viewer login required")
		}
		return desktopview.Identity{Owner: "native", Valid: func() bool { return ctx.Err() == nil }}, nil
	}})
	if err != nil {
		cancel()
		listener.Close()
		return nil, err
	}
	n.viewer = viewer
	n.server = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/native/bootstrap" {
			if r.Method != "GET" || r.Host != listener.Addr().String() || r.Header.Get("Origin") != "" && r.Header.Get("Origin") != n.origin {
				http.Error(w, "Origin rejected", 403)
				return
			}
			nonce := r.URL.Query().Get("nonce")
			n.mu.Lock()
			entry, ok := n.bootstrap[nonce]
			delete(n.bootstrap, nonce)
			n.mu.Unlock()
			if !ok || time.Now().After(entry.expires) {
				http.Error(w, "Bootstrap expired", 403)
				return
			}
			w.Header().Set("Referrer-Policy", "no-referrer")
			w.Header().Set("Cache-Control", "no-store")
			http.SetCookie(w, &http.Cookie{Name: n.cookieName, Value: n.secret, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
			http.Redirect(w, r, entry.path, http.StatusSeeOther)
			return
		}
		viewer.ServeHTTP(w, r)
	}), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: time.Minute, MaxHeaderBytes: 16384}
	go n.server.Serve(listener)
	return n, nil
}
func (n *nativeDesktop) call(ctx context.Context, path string, body []byte) (map[string]json.RawMessage, error) {
	r := httptest.NewRequest("POST", n.origin+path, bytes.NewReader(body)).WithContext(ctx)
	r.AddCookie(&http.Cookie{Name: n.cookieName, Value: n.secret})
	w := httptest.NewRecorder()
	n.viewer.ServeHTTP(w, r)
	if w.Code != 200 {
		return nil, errors.New("native desktop request failed: " + strings.TrimSpace(w.Body.String()))
	}
	var result map[string]json.RawMessage
	err := json.Unmarshal(w.Body.Bytes(), &result)
	return result, err
}
func (s *Service) OpenDesktop(ctx context.Context, sandboxID string) error {
	if s.native == nil {
		return errors.New("native desktop unavailable")
	}
	payload, _ := json.Marshal(map[string]string{"sandbox_id": sandboxID})
	result, err := s.native.call(ctx, "/api/v1/desktop/sessions", payload)
	if err != nil {
		return err
	}
	var path string
	if err = json.Unmarshal(result["path"], &path); err != nil {
		return err
	}
	if !strings.HasPrefix(path, "/desktop/") || !strings.HasSuffix(path, "/") {
		return errors.New("invalid native display path")
	}
	nonce := rand.Text()
	s.native.mu.Lock()
	for key, value := range s.native.bootstrap {
		if time.Now().After(value.expires) {
			delete(s.native.bootstrap, key)
		}
	}
	s.native.bootstrap[nonce] = nativeBootstrap{path: path, expires: time.Now().Add(time.Minute)}
	s.native.mu.Unlock()
	window := s.app.Window.NewWithOptions(application.WebviewWindowOptions{Name: "pot-desktop-" + nonce, Title: "Pot Desktop", Width: 1280, Height: 840, MinWidth: 640, MinHeight: 480, URL: s.native.origin + "/native/bootstrap?nonce=" + nonce})
	window.RegisterHook(events.Common.WindowClosing, func(*application.WindowEvent) {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, _ = s.native.call(ctx, path+"api/close", []byte(`{}`))
		}()
	})
	window.Show()
	window.Focus()
	return nil
}
func (n *nativeDesktop) close() {
	n.once.Do(func() {
		n.cancel()
		_ = n.viewer.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if n.server.Shutdown(ctx) != nil {
			_ = n.server.Close()
		}
	})
}
