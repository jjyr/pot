package spaces

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/instance"
)

var ErrIdentityChanged = errors.New("Space Control identity changed")
var ErrScopeChanged = errors.New("Space connection changed while request was in flight; inspect the original request before retrying")
var ErrDisconnected = errors.New("Space is disconnected")

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
}
type Catalog struct {
	ActiveSpaceID string  `json:"active_space_id"`
	Items         []Space `json:"items"`
}
type ConnectRequest struct {
	Endpoint  string `json:"endpoint"`
	Token     string `json:"token"`
	CAPEM     string `json:"ca_pem,omitempty"`
	ControlID string `json:"control_id,omitempty"`
	Name      string `json:"name,omitempty"`
}
type config struct {
	Version   int    `json:"version"`
	Endpoint  string `json:"endpoint"`
	Token     string `json:"token"`
	CAPEM     string `json:"ca_pem,omitempty"`
	ControlID string `json:"control_id"`
	Name      string `json:"name"`
	Enabled   bool   `json:"enabled"`
}
type Options struct {
	Path               string
	Local              Caller
	LocalID, LocalName string
	RefreshInterval    time.Duration
	OnChange           func(space string, topics json.RawMessage)
	// OnStreamReset runs before the first changed frame on each connection.
	// Consumers must baseline projections rather than notify reconnect history.
	OnStreamReset func(space string)
}
type entry struct {
	space      Space
	caller     Caller
	generation uint64
	ctx        context.Context
	cancel     context.CancelFunc
	enabled    bool
}
type Manager struct {
	mu            sync.Mutex
	configure     sync.Mutex
	options       Options
	ctx           context.Context
	cancel        context.CancelFunc
	local, remote *entry
	saved         *config
	active        string
	generation    uint64
	closed        bool
	wg            sync.WaitGroup
	Changes       core.Changes
}

func Open(ctx context.Context, options Options) (*Manager, error) {
	if options.Local == nil || !core.Identifier(options.LocalID) || options.Path == "" {
		return nil, errors.New("App Spaces requires a local Control, stable identity, and private configuration path")
	}
	if options.LocalName == "" {
		options.LocalName = "This Mac"
	}
	if options.RefreshInterval <= 0 {
		options.RefreshInterval = 10 * time.Second
	}
	ctx, cancel := context.WithCancel(ctx)
	m := &Manager{options: options, ctx: ctx, cancel: cancel, active: options.LocalID, generation: 1}
	localCtx, localCancel := context.WithCancel(ctx)
	m.local = &entry{space: Space{ID: options.LocalID, ControlID: options.LocalID, RuntimeID: options.LocalID, Name: options.LocalName, Kind: "local"}, caller: options.Local, generation: 1, ctx: localCtx, cancel: localCancel, enabled: true}
	saved, err := readConfig(options.Path)
	if err != nil {
		cancel()
		return nil, err
	}
	if saved != nil {
		client, err := NewHTTPClient(saved.Endpoint, saved.Token, saved.CAPEM, saved.ControlID)
		if err != nil {
			cancel()
			return nil, err
		}
		remoteCtx, remoteCancel := context.WithCancel(ctx)
		m.saved = saved
		m.generation++
		m.remote = &entry{space: Space{ID: saved.ControlID, ControlID: saved.ControlID, RuntimeID: saved.ControlID, Name: saved.Name, Kind: "remote", Error: ErrDisconnected.Error()}, caller: client, generation: m.generation, ctx: remoteCtx, cancel: remoteCancel, enabled: saved.Enabled}
	}
	m.Refresh(ctx)
	m.wg.Add(1)
	go m.refreshLoop()
	m.startWatch(m.local)
	if m.remote != nil && m.remote.enabled {
		m.startWatch(m.remote)
	}
	return m, nil
}
func readConfig(path string) (*config, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("App Control configuration must be a private regular file")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var saved config
	if err = core.DecodeJSON(body, &saved); err != nil {
		return nil, err
	}
	if saved.Version != 1 || !core.Identifier(saved.ControlID) || saved.Name == "" {
		return nil, errors.New("invalid App Control configuration")
	}
	return &saved, nil
}
func cloneSpace(value Space) Space { value.Assistants = bytes.Clone(value.Assistants); return value }
func (m *Manager) Snapshot() Catalog {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := Catalog{ActiveSpaceID: m.active, Items: []Space{cloneSpace(m.local.space)}}
	if m.remote != nil {
		result.Items = append(result.Items, cloneSpace(m.remote.space))
	}
	return result
}
func (m *Manager) entry(space string) (*entry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, errors.New("App Spaces closed")
	}
	var found *entry
	if space == "" || space == m.local.space.ID {
		found = m.local
	} else if m.remote != nil && space == m.remote.space.ID {
		found = m.remote
	}
	if found == nil {
		return nil, errors.New("Space is unavailable")
	}
	if !found.enabled {
		return nil, ErrDisconnected
	}
	return found, nil
}
func (m *Manager) current(value *entry) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return !m.closed && value.enabled && (m.local == value || m.remote == value) && value.ctx.Err() == nil
}
func (m *Manager) requestContext(ctx context.Context, value *entry) (context.Context, context.CancelFunc) {
	next, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(value.ctx, cancel)
	return next, func() { stop(); cancel() }
}

// Call routes by captured Control identity. It never uses the selected Chat or
// retries a failed remote operation against local services.
func (m *Manager) Call(ctx context.Context, space, method, path string, body json.RawMessage) (json.RawMessage, error) {
	value, err := m.entry(space)
	if err != nil {
		return nil, err
	}
	path, err = operatorPath(path)
	if err != nil {
		return nil, err
	}
	ctx, cancel := m.requestContext(ctx, value)
	defer cancel()
	reply, err := value.caller.Call(ctx, method, path, bytes.Clone(body))
	if !m.current(value) {
		return nil, ErrScopeChanged
	}
	return reply, err
}
func (m *Manager) Stream(ctx context.Context, space, path string, receive func(Event) error) error {
	value, err := m.entry(space)
	if err != nil {
		return err
	}
	path, err = operatorPath(path)
	if err != nil {
		return err
	}
	ctx, cancel := m.requestContext(ctx, value)
	defer cancel()
	err = value.caller.Stream(ctx, path, func(event Event) error {
		if !m.current(value) {
			return ErrScopeChanged
		}
		return receive(event)
	})
	if !m.current(value) {
		return ErrScopeChanged
	}
	return err
}
func (m *Manager) Select(space string) error {
	value, err := m.entry(space)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.active = value.space.ID
	m.mu.Unlock()
	m.Changes.Notify("spaces")
	return nil
}
func (m *Manager) Connect(ctx context.Context, request ConnectRequest) error {
	m.configure.Lock()
	defer m.configure.Unlock()
	m.mu.Lock()
	exists, closed := m.remote != nil, m.closed
	m.mu.Unlock()
	if closed {
		return errors.New("App Spaces closed")
	}
	if exists {
		return errors.New("App already has a remote Space; reconnect or remove it explicitly")
	}
	client, err := NewHTTPClient(request.Endpoint, request.Token, request.CAPEM, request.ControlID)
	if err != nil {
		return err
	}
	info, err := remoteIdentity(ctx, client)
	if err != nil {
		client.Close()
		return err
	}
	if request.ControlID != "" && request.ControlID != info.RuntimeID {
		client.Close()
		return ErrIdentityChanged
	}
	if info.RuntimeID == m.options.LocalID {
		client.Close()
		return errors.New("remote Space must differ from the embedded Control")
	}
	request.ControlID = info.RuntimeID
	if request.Name == "" {
		request.Name = info.Name
	}
	if request.Name == "" {
		request.Name = info.RuntimeID
	}
	client.expectedID = info.RuntimeID
	saved := &config{Version: 1, Endpoint: request.Endpoint, Token: request.Token, CAPEM: request.CAPEM, ControlID: request.ControlID, Name: request.Name, Enabled: true}
	if err = instance.WriteJSON(m.options.Path, saved); err != nil {
		client.Close()
		return err
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		client.Close()
		return errors.New("App Spaces closed")
	}
	m.generation++
	remoteCtx, cancel := context.WithCancel(m.ctx)
	value := &entry{space: Space{ID: info.RuntimeID, ControlID: info.RuntimeID, RuntimeID: info.RuntimeID, Name: request.Name, Kind: "remote"}, caller: client, generation: m.generation, ctx: remoteCtx, cancel: cancel, enabled: true}
	m.remote = value
	m.saved = saved
	m.mu.Unlock()
	m.refreshEntry(ctx, value)
	m.startWatch(value)
	m.Changes.Notify("spaces")
	return nil
}

type controlInfo struct {
	ID        string `json:"id"`
	RuntimeID string `json:"runtime_id"`
	Name      string `json:"name"`
	Role      string `json:"role"`
}

func remoteIdentity(ctx context.Context, caller Caller) (controlInfo, error) {
	var result controlInfo
	body, err := caller.Call(ctx, "GET", "/api/v1/control", nil)
	if err != nil {
		return result, err
	}
	if err = json.Unmarshal(body, &result); err != nil {
		return result, err
	}
	if result.Role != "control" || !core.Identifier(result.RuntimeID) || result.ID != result.RuntimeID {
		return result, errors.New("remote endpoint must be an explicitly enabled standalone Control")
	}
	return result, nil
}
func (m *Manager) Disconnect(space string) error { return m.setEnabled(space, false) }
func (m *Manager) Reconnect(ctx context.Context, space string) error {
	if err := m.setEnabled(space, true); err != nil {
		return err
	}
	value, err := m.entry(space)
	if err != nil {
		return err
	}
	m.refreshEntry(ctx, value)
	m.startWatch(value)
	return nil
}
func (m *Manager) setEnabled(space string, enabled bool) error {
	m.configure.Lock()
	defer m.configure.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.remote == nil || m.remote.space.ID != space {
		return errors.New("remote Space unavailable")
	}
	saved := *m.saved
	saved.Enabled = enabled
	if err := instance.WriteJSON(m.options.Path, saved); err != nil {
		return err
	}
	old := m.remote
	old.cancel()
	m.generation++
	ctx, cancel := context.WithCancel(m.ctx)
	value := *old
	value.ctx = ctx
	value.cancel = cancel
	value.generation = m.generation
	value.enabled = enabled
	value.space.Connected = false
	value.space.Error = ErrDisconnected.Error()
	m.remote = &value
	m.saved = &saved
	m.Changes.Notify("spaces")
	return nil
}
func (m *Manager) Remove(space string) error {
	m.configure.Lock()
	defer m.configure.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.remote == nil || m.remote.space.ID != space {
		return errors.New("remote Space unavailable")
	}
	if err := os.Remove(m.options.Path); err != nil && !os.IsNotExist(err) {
		return err
	}
	m.remote.cancel()
	if closer, ok := m.remote.caller.(interface{ Close() error }); ok {
		closer.Close()
	}
	m.remote = nil
	m.saved = nil
	m.generation++
	if m.active == space {
		m.active = m.local.space.ID
	}
	m.Changes.Notify("spaces")
	return nil
}
func (m *Manager) Refresh(ctx context.Context) {
	m.mu.Lock()
	entries := []*entry{m.local}
	if m.remote != nil && m.remote.enabled {
		entries = append(entries, m.remote)
	}
	m.mu.Unlock()
	var wait sync.WaitGroup
	for _, value := range entries {
		wait.Add(1)
		go func() { defer wait.Done(); m.refreshEntry(ctx, value) }()
	}
	wait.Wait()
}
func (m *Manager) refreshEntry(ctx context.Context, value *entry) {
	if !m.current(value) {
		return
	}
	ctx, cancel := m.requestContext(ctx, value)
	defer cancel()
	ctx, timeout := context.WithTimeout(ctx, 5*time.Second)
	defer timeout()
	var runtime struct {
		RuntimeID string `json:"runtime_id"`
		BootID    string `json:"boot_id"`
	}
	var assistants json.RawMessage
	body, err := value.caller.Call(ctx, "GET", "/api/v1/runtime", nil)
	if err == nil {
		err = json.Unmarshal(body, &runtime)
		if err == nil && (runtime.RuntimeID != value.space.ControlID || runtime.BootID == "") {
			err = ErrIdentityChanged
		}
	}
	if err == nil && value.space.Kind == "remote" {
		var info controlInfo
		info, err = remoteIdentity(ctx, value.caller)
		if err == nil && info.RuntimeID != value.space.ControlID {
			err = ErrIdentityChanged
		}
	}
	if err == nil {
		assistants, err = value.caller.Call(ctx, "GET", "/api/v1/assistants", nil)
		if err == nil && !json.Valid(assistants) {
			err = errors.New("invalid assistant catalogue")
		}
	}
	m.mu.Lock()
	if m.closed || !value.enabled || (m.local != value && m.remote != value) {
		m.mu.Unlock()
		return
	}
	previous, _ := json.Marshal(value.space)
	value.space.Connected = err == nil
	if err != nil {
		value.space.Error = err.Error()
	} else {
		value.space.Error = ""
		value.space.BootID = runtime.BootID
		value.space.Assistants = bytes.Clone(assistants)
	}
	next, _ := json.Marshal(value.space)
	id := value.space.ID
	m.mu.Unlock()
	if !bytes.Equal(previous, next) {
		m.Changes.Notify("spaces/" + id)
	}
}
func (m *Manager) refreshLoop() {
	defer m.wg.Done()
	timer := time.NewTicker(m.options.RefreshInterval)
	defer timer.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-timer.C:
			m.mu.Lock()
			entries := []*entry{}
			for _, value := range []*entry{m.local, m.remote} {
				if value != nil && value.enabled && !value.space.Connected {
					entries = append(entries, value)
				}
			}
			m.mu.Unlock()
			var wait sync.WaitGroup
			for _, value := range entries {
				wait.Add(1)
				go func() { defer wait.Done(); m.refreshEntry(m.ctx, value) }()
			}
			wait.Wait()
		}
	}
}
func (m *Manager) startWatch(value *entry) {
	m.mu.Lock()
	if m.closed || !value.enabled {
		m.mu.Unlock()
		return
	}
	m.wg.Add(1)
	m.mu.Unlock()
	go func() {
		defer m.wg.Done()
		for {
			var versions map[string]json.RawMessage
			if !m.current(value) {
				return
			}
			err := value.caller.Stream(value.ctx, "/api/v1/changes", func(event Event) error {
				if !m.current(value) {
					return ErrScopeChanged
				}
				if versions == nil && m.options.OnStreamReset != nil {
					m.options.OnStreamReset(value.space.ID)
				}
				changed, next, hasChanges := changedEvent(event, versions)
				versions = next
				if !hasChanges {
					return nil
				}
				event = changed
				if catalogDirty(event) {
					m.refreshEntry(value.ctx, value)
				}
				if m.options.OnChange != nil {
					m.options.OnChange(value.space.ID, bytes.Clone(event.Data))
				}
				return nil
			})
			if !m.current(value) {
				return
			}
			if err != nil {
				m.mu.Lock()
				if m.local == value || m.remote == value {
					value.space.Connected = false
					value.space.Error = err.Error()
				}
				m.mu.Unlock()
				m.Changes.Notify("spaces/" + value.space.ID)
			}
			select {
			case <-value.ctx.Done():
				return
			case <-time.After(time.Second):
			}
		}
	}()
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
		if strings.HasPrefix(topic, "assistant") || strings.HasPrefix(topic, "conversation") || strings.HasPrefix(topic, "runtime") || topic == "boot_id" {
			return true
		}
	}
	return false
}
func (m *Manager) Space(id string) (Space, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, value := range []*entry{m.local, m.remote} {
		if value != nil && value.space.ID == id {
			return cloneSpace(value.space), nil
		}
	}
	return Space{}, errors.New("Space unavailable")
}
func (m *Manager) RefreshSpace(ctx context.Context, id string) error {
	value, err := m.entry(id)
	if err != nil {
		return err
	}
	m.refreshEntry(ctx, value)
	return nil
}

func (m *Manager) Close() error {
	m.configure.Lock()
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		m.configure.Unlock()
		return nil
	}
	m.closed = true
	m.cancel()
	m.local.cancel()
	if m.remote != nil {
		m.remote.cancel()
	}
	remote := m.remote
	m.mu.Unlock()
	m.configure.Unlock()
	m.wg.Wait()
	if remote != nil {
		if closer, ok := remote.caller.(interface{ Close() error }); ok {
			return closer.Close()
		}
	}
	return nil
}

// AssistantCatalog returns a copy of each Space's last confirmed catalogue.
// Consumers keep the provenance key even when display names or list order match.
func (m *Manager) AssistantCatalog() []struct {
	SpaceID   string
	Connected bool
	Items     json.RawMessage
} {
	catalog := m.Snapshot()
	out := []struct {
		SpaceID   string
		Connected bool
		Items     json.RawMessage
	}{}
	sort.SliceStable(catalog.Items, func(i, j int) bool { return catalog.Items[i].Kind == "local" && catalog.Items[j].Kind != "local" })
	for _, space := range catalog.Items {
		out = append(out, struct {
			SpaceID   string
			Connected bool
			Items     json.RawMessage
		}{space.ID, space.Connected, bytes.Clone(space.Assistants)})
	}
	return out
}
func ConfigurationPath(root string) string { return filepath.Join(root, "app-control.json") }
