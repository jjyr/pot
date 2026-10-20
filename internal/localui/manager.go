// Package localui projects the autonomous local node for the native application.
// UI scope is transient presentation state, never remote owner authority.
package localui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/pkg/core"
	"strings"
	"sync"
	"time"
)

var ErrScopeChanged = errors.New("local UI scope is unavailable")

type Space struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Kind       string          `json:"kind"`
	ControlID  string          `json:"control_id"`
	RuntimeID  string          `json:"runtime_id"`
	BootID     string          `json:"boot_id"`
	Connected  bool            `json:"connected"`
	Error      string          `json:"error,omitempty"`
	Assistants json.RawMessage `json:"assistants,omitempty"`
	// Native attention reads this separate projection; frontend directory rows
	// continue to come from Agent Link and are not duplicated in /spaces.
	LinkedAssistants json.RawMessage `json:"-"`
}
type Catalog struct {
	ActiveSpaceID string  `json:"active_space_id"`
	Items         []Space `json:"items"`
}
type Options struct {
	Local              Caller
	LocalID, LocalName string
	OnChange           func(string, json.RawMessage)
	OnStreamReset      func(string)
}
type Manager struct {
	mu      sync.Mutex
	options Options
	ctx     context.Context
	cancel  context.CancelFunc
	local   Space
	wg      sync.WaitGroup
	Changes core.Changes
}

func Open(ctx context.Context, options Options) (*Manager, error) {
	if options.Local == nil || !core.Identifier(options.LocalID) {
		return nil, errors.New("local UI requires a node and request adapter")
	}
	if options.LocalName == "" {
		options.LocalName = "Pot"
	}
	ctx, cancel := context.WithCancel(ctx)
	m := &Manager{ctx: ctx, cancel: cancel, options: options, local: Space{ID: options.LocalID, ControlID: options.LocalID, RuntimeID: options.LocalID, Name: options.LocalName, Kind: "local"}}
	m.Refresh(ctx)
	m.wg.Add(1)
	go m.watch()
	return m, nil
}
func cloneSpace(value Space) Space {
	value.Assistants = bytes.Clone(value.Assistants)
	value.LinkedAssistants = bytes.Clone(value.LinkedAssistants)
	return value
}
func (m *Manager) Snapshot() Catalog {
	m.mu.Lock()
	defer m.mu.Unlock()
	return Catalog{m.local.ID, []Space{cloneSpace(m.local)}}
}
func (m *Manager) Space(id string) (Space, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id != "" && id != m.local.ID {
		return Space{}, ErrScopeChanged
	}
	return cloneSpace(m.local), nil
}
func (m *Manager) Call(ctx context.Context, scope, method, path string, body json.RawMessage) (json.RawMessage, error) {
	if _, err := m.Space(scope); err != nil {
		return nil, err
	}
	path, err := operatorPath(path)
	if err != nil {
		return nil, err
	}
	return m.options.Local.Call(ctx, method, path, bytes.Clone(body))
}
func (m *Manager) Stream(ctx context.Context, scope, path string, receive func(Event) error) error {
	if _, err := m.Space(scope); err != nil {
		return err
	}
	path, err := operatorPath(path)
	if err != nil {
		return err
	}
	return m.options.Local.Stream(ctx, path, receive)
}
func (m *Manager) Refresh(ctx context.Context) {
	var runtime struct {
		RuntimeID string `json:"runtime_id"`
		BootID    string `json:"boot_id"`
	}
	data, err := m.options.Local.Call(ctx, "GET", "/api/v1/runtime", nil)
	if err == nil {
		err = json.Unmarshal(data, &runtime)
		if err == nil && (runtime.RuntimeID != m.options.LocalID || runtime.BootID == "") {
			err = errors.New("local node identity changed")
		}
	}
	var assistants json.RawMessage
	if err == nil {
		assistants, err = m.options.Local.Call(ctx, "GET", "/api/v1/assistants", nil)
		if err == nil && !json.Valid(assistants) {
			err = errors.New("invalid local assistant directory")
		}
	}
	var linked json.RawMessage
	if err == nil {
		// A failed peer-directory read does not make local assistants unavailable.
		// Drop only the native peer attention projection until a confirmed refresh.
		if raw, e := m.options.Local.Call(ctx, "GET", "/api/v1/agent-links", nil); e == nil {
			linked, _ = projectLinkedAttention(raw)
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.local.Connected = err == nil
	if err != nil {
		m.local.Error = err.Error()
	} else {
		m.local.Error = ""
		m.local.BootID = runtime.BootID
		m.local.Assistants = bytes.Clone(assistants)
		m.local.LinkedAssistants = bytes.Clone(linked)
	}
}
func (m *Manager) watch() {
	defer m.wg.Done()
	for m.ctx.Err() == nil {
		var versions map[string]json.RawMessage
		_ = m.options.Local.Stream(m.ctx, "/api/v1/changes", func(event Event) error {
			if versions == nil && m.options.OnStreamReset != nil {
				m.options.OnStreamReset(m.options.LocalID)
			}
			changed, next, dirty := changedEvent(event, versions)
			versions = next
			if !dirty {
				return nil
			}
			if catalogDirty(changed) {
				m.Refresh(m.ctx)
				m.Changes.Notify("local-ui")
			}
			if m.options.OnChange != nil {
				m.options.OnChange(m.options.LocalID, bytes.Clone(changed.Data))
			}
			return nil
		})
		select {
		case <-m.ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}
func (m *Manager) Close() error {
	m.cancel()
	m.wg.Wait()
	if closer, ok := m.options.Local.(interface{ Close() error }); ok {
		return closer.Close()
	}
	return nil
}

// Change streams carry topic versions, not just dirty keys. Compare versions
// before refreshing so unrelated usage events cannot repoll the catalogue/mail.
func changedEvent(event Event, previous map[string]json.RawMessage) (Event, map[string]json.RawMessage, bool) {
	var raw map[string]json.RawMessage
	if json.Unmarshal(event.Data, &raw) != nil {
		return event, previous, true
	}
	current := raw
	if nested := raw["versions"]; nested != nil {
		if json.Unmarshal(nested, &current) != nil {
			return event, previous, true
		}
		if boot := raw["boot_id"]; boot != nil {
			current["boot_id"] = boot
		}
	}
	dirty := map[string]json.RawMessage{}
	bootChanged := !bytes.Equal(current["boot_id"], previous["boot_id"])
	for key, value := range current {
		if previous == nil || bootChanged || !bytes.Equal(value, previous[key]) {
			dirty[key] = value
		}
	}
	if previous != nil && len(dirty) == 0 {
		return event, current, false
	}
	data, _ := json.Marshal(dirty)
	event.Data = data
	return event, current, true
}
func catalogDirty(event Event) bool {
	var topics map[string]json.RawMessage
	if json.Unmarshal(event.Data, &topics) != nil {
		return true
	}
	if nested := topics["versions"]; nested != nil {
		if json.Unmarshal(nested, &topics) != nil {
			return true
		}
	}
	if len(topics) == 0 {
		return true
	}
	for topic := range topics {
		if strings.HasPrefix(topic, "assistant") || strings.HasPrefix(topic, "conversation") || strings.HasPrefix(topic, "runtime") || topic == "boot_id" || topic == "agent-links" {
			return true
		}
	}
	return false
}
