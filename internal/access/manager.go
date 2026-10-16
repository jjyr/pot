// Package access owns optional browser publication for a local product instance.
package access

import (
	"context"
	"encoding/base64"
	"errors"
	"github.com/jjyr/pot/internal/browserpush"
	"github.com/jjyr/pot/internal/webui"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/instance"
	qrcode "github.com/skip2/go-qrcode"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Tailnet interface {
	Connect(context.Context) error
	Status(context.Context) (any, error)
	SetWeb(context.Context, int, bool) (string, error)
	Close() error
}
type Settings struct {
	Connected bool `json:"connected"`
	Enabled   bool `json:"enabled"`
}
type Options struct {
	Root     string
	Origin   string
	Upstream webui.Upstream
	Assets   fs.FS
	Tailnet  Tailnet
	Changes  *core.Changes
}
type Manager struct {
	op            sync.Mutex
	mu            sync.Mutex
	options       Options
	settings      Settings
	store         webui.AccessStore
	browser       *webui.Server
	browserCancel context.CancelFunc
	http          *http.Server
	listener      net.Listener
	origin        string
	port          int
	ctx           context.Context
	cancel        context.CancelFunc
	workers       sync.WaitGroup
	closed        bool
	lastError     string
	applied       uint64
}

func Open(ctx context.Context, o Options) (*Manager, error) {
	if o.Tailnet == nil {
		return nil, errors.New("tailnet service required")
	}
	ctx, cancel := context.WithCancel(ctx)
	m := &Manager{options: o, store: webui.AccessStore{Path: filepath.Join(o.Root, "browser-access.json")}, ctx: ctx, cancel: cancel}
	if data, err := os.ReadFile(filepath.Join(o.Root, "access-settings.json")); err == nil {
		if err = core.DecodeJSON(data, &m.settings); err != nil {
			cancel()
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		cancel()
		return nil, err
	}
	_, existed := os.Stat(m.store.Path)
	config, err := m.store.Load(true)
	if err != nil {
		cancel()
		return nil, err
	}
	if os.IsNotExist(existed) && o.Origin == "" && !m.settings.Enabled && !config.Disabled {
		config, err = m.store.Change("disable")
		if err != nil {
			cancel()
			return nil, err
		}
	}
	m.settings.Enabled = !config.Disabled
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		cancel()
		return nil, err
	}
	m.listener = listener
	m.port = listener.Addr().(*net.TCPAddr).Port
	m.origin = "http://" + listener.Addr().String()
	if o.Origin != "" {
		m.origin = o.Origin
	}
	m.http = &http.Server{Handler: http.HandlerFunc(m.ServeHTTP), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: time.Minute, MaxHeaderBytes: 16384}
	if err = m.replaceBrowser(m.origin, config); err != nil {
		listener.Close()
		cancel()
		return nil, err
	}
	m.workers.Go(func() { _ = m.http.Serve(listener) })
	// Only a prior explicit owner connection is restored. Fresh roots do no
	// Tailscale network work and do not publish browser access.
	if m.settings.Connected {
		m.workers.Go(func() {
			m.op.Lock()
			defer m.op.Unlock()
			if ctx.Err() != nil {
				return
			}
			if err := o.Tailnet.Connect(ctx); err != nil {
				m.recordError(err)
				return
			}
			if m.settings.Enabled {
				if err := m.enable(ctx); err != nil {
					m.recordError(err)
				}
			}
		})
	}
	return m, nil
}
func (m *Manager) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	browser := m.browser
	m.mu.Unlock()
	if browser == nil {
		http.Error(w, "browser unavailable", 503)
		return
	}
	browser.ServeHTTP(w, r)
}
func (m *Manager) recordError(err error) {
	m.mu.Lock()
	m.lastError = err.Error()
	m.mu.Unlock()
	m.changed()
}
func (m *Manager) changed() {
	if m.options.Changes != nil {
		m.options.Changes.Notify("access")
	}
}
func (m *Manager) replaceBrowser(origin string, config webui.Config) error {
	browser, err := webui.New(webui.Options{Origin: origin, Config: config, Upstream: m.options.Upstream, Assets: m.options.Assets, Persist: m.store.SaveSessions})
	if err != nil {
		return err
	}
	sender, err := browserpush.FromEnvironment()
	if err != nil {
		browser.Close()
		return err
	}
	var push browserpush.Sender
	if sender != nil {
		push = sender
	}
	if err = browser.AttachPush(m.options.Root, webui.NotificationSource{ID: m.options.Upstream.ControlID, Upstream: m.options.Upstream}, push); err != nil {
		browser.Close()
		return err
	}
	runctx, cancel := context.WithCancel(m.ctx)
	m.mu.Lock()
	previous, previousCancel := m.browser, m.browserCancel
	m.browser = browser
	m.browserCancel = cancel
	m.origin = origin
	m.applied = config.Generation
	m.mu.Unlock()
	if previousCancel != nil {
		previousCancel()
	}
	if previous != nil {
		previous.Close()
	}
	m.workers.Go(func() { _ = browser.RunNotifications(runctx) })
	return nil
}
func (m *Manager) enable(ctx context.Context) error {
	origin := m.options.Origin
	var err error
	if m.settings.Connected {
		origin, err = m.options.Tailnet.SetWeb(ctx, m.port, true)
		if err != nil {
			return err
		}
	} else if origin == "" {
		return errors.New("connect Tailscale or configure an explicit browser origin first")
	}
	config, err := m.store.Change("enable")
	if err != nil {
		m.options.Tailnet.SetWeb(ctx, m.port, false)
		return err
	}
	if err = m.replaceBrowser(origin, config); err != nil {
		m.options.Tailnet.SetWeb(ctx, m.port, false)
		return err
	}
	m.settings.Enabled = true
	return instance.WriteJSON(filepath.Join(m.options.Root, "access-settings.json"), m.settings)
}
func (m *Manager) Call(ctx context.Context, method string, body []byte) (any, error) {
	m.op.Lock()
	defer m.op.Unlock()
	if m.closed {
		return nil, errors.New("access manager closed")
	}
	if method == "POST" {
		var request struct {
			Action string `json:"action" required:"true"`
		}
		if err := core.DecodeJSON(body, &request); err != nil {
			return nil, err
		}
		switch request.Action {
		case "token":
		case "regenerate_token":
			config, err := m.store.Change("regenerate-token")
			if err != nil {
				return nil, err
			}
			m.mu.Lock()
			browser := m.browser
			m.mu.Unlock()
			if err = browser.ReplaceAccess(config); err != nil {
				return nil, err
			}
			m.applied = config.Generation
		case "connect":
			if err := m.options.Tailnet.Connect(ctx); err != nil {
				return nil, err
			}
			m.settings.Connected = true
			if err := instance.WriteJSON(filepath.Join(m.options.Root, "access-settings.json"), m.settings); err != nil {
				return nil, err
			}
		case "enable":
			if err := m.enable(ctx); err != nil {
				return nil, err
			}
		case "disable", "disconnect":
			config, err := m.store.Change("disable")
			if err != nil {
				return nil, err
			}
			m.mu.Lock()
			browser := m.browser
			m.mu.Unlock()
			if err = browser.ReplaceAccess(config); err != nil {
				return nil, err
			}
			m.applied = config.Generation
			if _, err = m.options.Tailnet.SetWeb(ctx, m.port, false); err != nil {
				return nil, err
			}
			m.settings.Enabled = false
			if request.Action == "disconnect" {
				if err = m.options.Tailnet.Close(); err != nil {
					return nil, err
				}
				m.settings.Connected = false
			}
			if err = instance.WriteJSON(filepath.Join(m.options.Root, "access-settings.json"), m.settings); err != nil {
				return nil, err
			}
		default:
			return nil, errors.New("unknown access action")
		}
		m.changed()
	} else if method != "GET" {
		return nil, errors.New("unsupported access method")
	}
	config, err := m.store.Load(false)
	if err != nil {
		return nil, err
	}
	status, err := m.options.Tailnet.Status(ctx)
	if err != nil {
		status = map[string]any{"state": "Error", "error": err.Error()}
	}
	m.mu.Lock()
	origin, lastError := m.origin, m.lastError
	m.mu.Unlock()
	result := map[string]any{"enabled": !config.Disabled, "origin": origin, "token": config.Token, "generation": config.Generation, "tailscale": status, "error": lastError}
	if !config.Disabled {
		connection := origin + "/#access_token=" + url.QueryEscape(config.Token)
		result["connection_url"] = connection
		image, err := qrcode.Encode(connection, qrcode.Medium, 256)
		if err == nil {
			result["qr_data_url"] = "data:image/png;base64," + base64.StdEncoding.EncodeToString(image)
		}
	}
	return result, nil
}
func (m *Manager) Close() error {
	m.op.Lock()
	if m.closed {
		m.op.Unlock()
		return nil
	}
	m.closed = true
	m.cancel()
	m.mu.Lock()
	browser := m.browser
	m.browser = nil
	m.mu.Unlock()
	if browser != nil {
		browser.Close()
	}
	err := errors.Join(m.http.Close(), m.options.Tailnet.Close())
	m.op.Unlock()
	m.workers.Wait()
	return err
}
