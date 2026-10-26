// Package access owns browser authentication and local owner administration.
package access

import (
	"context"
	"encoding/base64"
	"errors"
	"io/fs"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"

	"github.com/jjyr/pot/internal/webui"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/storage/migrations"
	qrcode "github.com/skip2/go-qrcode"
)

type Options struct {
	Root     string
	Origin   string
	Upstream webui.Upstream
	Assets   fs.FS
	Changes  *core.Changes
}
type Manager struct {
	op      sync.Mutex
	mu      sync.Mutex
	options Options
	store   webui.AccessStore
	browser *webui.Server
	applied uint64
	closed  bool
}

func Open(o Options) (*Manager, error) {
	if o.Origin == "" {
		return nil, errors.New("explicit browser origin required")
	}
	m := &Manager{options: o, store: webui.AccessStore{Path: filepath.Join(o.Root, "browser-access.json")}}
	config, err := m.store.Load(true)
	if err != nil {
		return nil, err
	}
	m.browser, err = webui.New(webui.Options{Root: o.Root, Origin: o.Origin, Config: config, Upstream: o.Upstream, Assets: o.Assets, Persist: m.store.SaveSessions})
	if err != nil {
		return nil, err
	}
	if err = migrations.RetireBrowserTailnet(o.Root); err != nil {
		m.browser.Close()
		return nil, err
	}
	m.applied = config.Generation
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
func (m *Manager) changed() {
	if m.options.Changes != nil {
		m.options.Changes.Notify("access")
	}
}
func (m *Manager) Call(_ context.Context, method string, body []byte) (any, error) {
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
		case "regenerate_token", "enable", "disable":
			config, err := m.store.Change(strings.ReplaceAll(request.Action, "_", "-"))
			if err != nil {
				return nil, err
			}
			if err = m.browser.ReplaceAccess(config); err != nil {
				return nil, err
			}
			m.applied = config.Generation
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
	result := map[string]any{"enabled": !config.Disabled, "origin": m.options.Origin, "token": config.Token, "generation": config.Generation}
	if !config.Disabled {
		connection := m.options.Origin + "/#access_token=" + url.QueryEscape(config.Token)
		result["connection_url"] = connection
		if image, err := qrcode.Encode(connection, qrcode.Medium, 256); err == nil {
			result["qr_data_url"] = "data:image/png;base64," + base64.StdEncoding.EncodeToString(image)
		}
	}
	return result, nil
}
func (m *Manager) Close() error {
	m.op.Lock()
	defer m.op.Unlock()
	if m.closed {
		return nil
	}
	m.closed = true
	m.mu.Lock()
	browser := m.browser
	m.browser = nil
	m.mu.Unlock()
	if browser != nil {
		browser.Close()
	}
	return nil
}
