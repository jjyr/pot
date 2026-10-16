// Package resourcecontrol owns explicit owner consent, durable bundle pins and
// preparation requests. Resource verification and Sandbox lifecycle remain ports.
package resourcecontrol

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/jjyr/pot/internal/core"
	"github.com/jjyr/pot/internal/resource"
	"github.com/jjyr/pot/internal/sandbox"
	"github.com/jjyr/pot/internal/sandboxresources"
	"github.com/jjyr/pot/internal/storage"
)

type Options struct {
	Root            string
	Catalog         sandboxresources.Catalog
	Store           *resource.Store
	Repository      string
	Bind            func(context.Context, string, sandbox.PreparedResources) error
	ValidateSandbox func(context.Context, string) error
	Notify          func()
}
type Request struct {
	ID         string            `json:"id"`
	SandboxID  string            `json:"sandbox_id"`
	BundleID   string            `json:"bundle_id"`
	Owner      string            `json:"owner,omitempty"`
	Turn       string            `json:"turn,omitempty"`
	State      string            `json:"state"`
	Error      string            `json:"error,omitempty"`
	ResourceID string            `json:"resource_id,omitempty"`
	Progress   resource.Progress `json:"progress"`
}
type ResourceStatus struct {
	ID string `json:"id"`
	resource.Resource
	Cached bool   `json:"cached"`
	Error  string `json:"error,omitempty"`
}
type BundleStatus struct {
	ID           string           `json:"id"`
	Resources    []ResourceStatus `json:"resources"`
	MissingBytes uint64           `json:"missing_bytes"`
	Ready        bool             `json:"ready"`
}
type Status struct {
	DefaultBundle string         `json:"default_bundle"`
	Bundles       []BundleStatus `json:"bundles"`
	Requests      []Request      `json:"requests"`
	Cache         CacheStatus    `json:"cache"`
}
type entry struct {
	request Request
	cancel  context.CancelFunc
}
type Manager struct {
	options Options
	db      *sql.DB
	ctx     context.Context
	cancel  context.CancelFunc
	mu      sync.Mutex
	entries map[string]*entry
	changed chan struct{}
	workers sync.WaitGroup
	closed  bool
}

func Open(options Options) (*Manager, error) {
	if options.Store == nil || options.Bind == nil {
		return nil, errors.New("resource Store and binding port required")
	}
	if err := options.Catalog.Validate(); err != nil {
		return nil, err
	}
	if options.Repository == "" {
		options.Repository = "https://github.com/jjyr/pot"
	}
	if err := os.MkdirAll(options.Root, 0700); err != nil {
		return nil, err
	}
	db, err := storage.OpenSQLite(filepath.Join(options.Root, "sandbox-resources.sqlite"))
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{options: options, db: db, ctx: ctx, cancel: cancel, entries: map[string]*entry{}, changed: make(chan struct{})}
	var version int
	if err = db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		cancel()
		db.Close()
		return nil, err
	}
	if version == 0 {
		var legacy int
		if err = db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='resource_requests'`).Scan(&legacy); err != nil || legacy != 0 {
			cancel()
			db.Close()
			return nil, errors.New("resource request schema requires a fresh Go root")
		}
	} else if version != 2 {
		cancel()
		db.Close()
		return nil, errors.New("unsupported resource request schema")
	}
	if _, err = db.Exec(`CREATE TABLE IF NOT EXISTS resource_requests (id TEXT PRIMARY KEY,sandbox_id TEXT NOT NULL,body TEXT NOT NULL); PRAGMA user_version=2`); err == nil {
		var rows *sql.Rows
		rows, err = db.Query(`SELECT body FROM resource_requests`)
		if err == nil {
			for rows.Next() {
				var body string
				var r Request
				if err = rows.Scan(&body); err != nil {
					break
				}
				if err = core.DecodeJSON([]byte(body), &r); err != nil {
					break
				}
				if !core.Identifier(r.ID) || !core.Identifier(r.SandboxID) || (r.Owner != "" && !core.Identifier(r.Owner)) || (r.Turn != "" && !core.Identifier(r.Turn)) || options.Catalog.Bundles[r.BundleID].Image == "" {
					err = errors.New("invalid persisted resource request")
					break
				}
				if r.State == "preparing" {
					r.State = "interrupted"
					r.Error = "Preparation interrupted; explicitly continue or import verified resources"
				}
				m.entries[r.ID] = &entry{request: r}
			}
			if err == nil {
				err = rows.Err()
			}
			rows.Close()
		}
	}
	if err == nil {
		for _, e := range m.entries {
			if err = m.saveLocked(e); err != nil {
				break
			}
		}
	}
	if err != nil {
		cancel()
		db.Close()
		return nil, err
	}
	return m, nil
}
func (m *Manager) saveLocked(e *entry) error {
	r := e.request
	r.Progress = resource.Progress{}
	r.ResourceID = ""
	body, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = m.db.Exec(`INSERT INTO resource_requests VALUES (?,?,?) ON CONFLICT(id) DO UPDATE SET body=excluded.body`, r.ID, r.SandboxID, string(body))
	return err
}
func (m *Manager) changedLocked() { close(m.changed); m.changed = make(chan struct{}) }
func (m *Manager) notify() {
	if m.options.Notify != nil {
		m.options.Notify()
	}
}

// Snapshot only verifies cache bytes; it never starts a transfer or a VM.
func (m *Manager) Snapshot(ctx context.Context) (Status, error) {
	out := Status{DefaultBundle: m.options.Catalog.DefaultLinuxBundle, Bundles: []BundleStatus{}, Requests: []Request{}}
	for id, b := range m.options.Catalog.Bundles {
		item := BundleStatus{ID: id, Ready: true, Resources: []ResourceStatus{}}
		for _, rid := range []string{b.Kernel, b.Boot, b.Image} {
			r := m.options.Catalog.Resources[rid]
			_, cached, err := m.options.Store.Cached(ctx, r)
			diagnostic := ""
			if err != nil {
				cached = false
				if ctx.Err() != nil {
					return out, ctx.Err()
				}
				diagnostic = err.Error()
			}
			item.Resources = append(item.Resources, ResourceStatus{ID: rid, Resource: r, Cached: cached, Error: diagnostic})
			if !cached {
				item.Ready = false
				item.MissingBytes += r.SizeBytes
			}
		}
		out.Bundles = append(out.Bundles, item)
	}
	m.mu.Lock()
	for _, e := range m.entries {
		out.Requests = append(out.Requests, e.request)
	}
	m.mu.Unlock()
	sort.Slice(out.Bundles, func(i, j int) bool { return out.Bundles[i].ID < out.Bundles[j].ID })
	sort.Slice(out.Requests, func(i, j int) bool { return out.Requests[i].ID < out.Requests[j].ID })
	var err error
	out.Cache, err = m.cache(ctx)
	return out, err
}

// Request persists an immutable instance pin. Verified cached resources may be
// bound immediately; missing resources only create an awaiting-consent request.
func (m *Manager) Request(ctx context.Context, sandboxID, bundleID string) (Request, error) {
	return m.requestFor(ctx, sandboxID, bundleID, "", "")
}
func (m *Manager) requestFor(ctx context.Context, sandboxID, bundleID, owner, turn string) (Request, error) {
	if !core.Identifier(sandboxID) {
		return Request{}, errors.New("invalid Sandbox identity")
	}
	if (owner != "" && !core.Identifier(owner)) || (turn != "" && !core.Identifier(turn)) {
		return Request{}, errors.New("invalid resource execution identity")
	}
	if bundleID == "" {
		bundleID = m.options.Catalog.DefaultLinuxBundle
	}
	if _, ok := m.options.Catalog.Bundles[bundleID]; !ok {
		return Request{}, errors.New("unknown resource bundle")
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return Request{}, errors.New("resource manager closed")
	}
	if m.options.ValidateSandbox != nil {
		if err := m.options.ValidateSandbox(ctx, sandboxID); err != nil {
			m.mu.Unlock()
			return Request{}, err
		}
	}
	bound := false
	var ready *Request
	for _, e := range m.entries {
		if e.request.SandboxID == sandboxID {
			if e.request.BundleID != bundleID {
				m.mu.Unlock()
				return Request{}, errors.New("Sandbox resource bundle is pinned; create another Sandbox to change it")
			}
			bound = bound || e.request.State == "ready"
			if e.request.State == "ready" {
				value := e.request
				ready = &value
			}
			if e.request.Owner == owner && e.request.Turn == turn && e.request.State != "cancelled" {
				r := e.request
				m.mu.Unlock()
				return r, nil
			}
		}
	}
	// Once a Sandbox is bound, later execution scopes need no new consent row.
	if ready != nil && owner != "" {
		r := *ready
		m.mu.Unlock()
		return r, nil
	}
	e := &entry{request: Request{ID: rand.Text(), SandboxID: sandboxID, BundleID: bundleID, Owner: owner, Turn: turn, State: "awaiting_consent"}}
	if err := m.saveLocked(e); err != nil {
		m.mu.Unlock()
		return Request{}, err
	}
	// The first cache check/bind is serialized with owner actions so a newly
	// published request cannot race a download or cancellation during binding.
	p, cacheErr := m.options.Catalog.Cached(ctx, m.options.Store, bundleID)
	var bindErr error
	if cacheErr == nil {
		if !bound {
			bindErr = m.options.Bind(ctx, sandboxID, p)
		}
		if bindErr == nil {
			e.request.State = "ready"
		} else {
			e.request.State = "failed"
			e.request.Error = bindErr.Error()
		}
		if err := m.saveLocked(e); err != nil {
			m.mu.Unlock()
			return Request{}, err
		}
	}
	m.entries[e.request.ID] = e
	m.changedLocked()
	r := e.request
	m.mu.Unlock()
	m.notify()
	if ctx.Err() != nil {
		return r, ctx.Err()
	}
	return r, bindErr
}

// Wait keeps the original live operation pending; cancellation never replays it.
func (m *Manager) Wait(ctx context.Context, sandboxID, bundleID string) error {
	return m.WaitFor(ctx, sandboxID, bundleID, "", "")
}
func (m *Manager) WaitFor(ctx context.Context, sandboxID, bundleID, owner, turn string) error {
	r, err := m.requestFor(ctx, sandboxID, bundleID, owner, turn)
	if err != nil {
		return err
	}
	for {
		m.mu.Lock()
		e := m.entries[r.ID]
		if e == nil {
			m.mu.Unlock()
			return errors.New("Sandbox resource request was removed")
		}
		r = e.request
		changed := m.changed
		closed := m.closed
		m.mu.Unlock()
		if closed {
			return errors.New("resource manager closed")
		}
		switch r.State {
		case "ready":
			return nil
		case "cancelled", "failed", "interrupted":
			return errors.New("Sandbox resources " + r.State + ": " + r.Error)
		}
		select {
		case <-ctx.Done():
			m.Cancel(r.ID)
			return ctx.Err()
		case <-changed:
		}
	}
}

// Start is called only after an explicit native/private owner download action.
func (m *Manager) Start(id string) (Request, error) { return m.start(id, "") }
func (m *Manager) Import(id, directory string) (Request, error) {
	if !filepath.IsAbs(directory) {
		return Request{}, errors.New("absolute offline resource directory required")
	}
	return m.start(id, directory)
}
func (m *Manager) start(id, directory string) (Request, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return Request{}, errors.New("resource manager closed")
	}
	e := m.entries[id]
	if e == nil {
		m.mu.Unlock()
		return Request{}, errors.New("resource request not found")
	}
	if e.cancel != nil || e.request.State == "ready" {
		r := e.request
		m.mu.Unlock()
		return r, nil
	}
	previous := e.request
	e.request.State = "preparing"
	e.request.Error = ""
	e.request.Progress = resource.Progress{}
	e.request.ResourceID = ""
	if err := m.saveLocked(e); err != nil {
		e.request = previous
		m.mu.Unlock()
		return Request{}, err
	}
	ctx, cancel := context.WithCancel(m.ctx)
	e.cancel = cancel
	m.workers.Add(1)
	m.changedLocked()
	r := e.request
	m.mu.Unlock()
	m.notify()
	go m.run(ctx, e, r, directory)
	return r, nil
}
func (m *Manager) run(ctx context.Context, e *entry, request Request, directory string) {
	defer m.workers.Done()
	var prepared sandbox.PreparedResources
	var err error
	if directory != "" {
		prepared, err = m.options.Catalog.Import(ctx, m.options.Store, request.BundleID, directory)
	} else {
		prepared, err = m.options.Catalog.Prepare(ctx, m.options.Store, request.BundleID, m.options.Repository, func(id string, p resource.Progress) {
			m.mu.Lock()
			if e.request.State == "preparing" {
				e.request.ResourceID = id
				e.request.Progress = p
				m.changedLocked()
			}
			m.mu.Unlock()
			m.notify()
		})
	}
	if err == nil {
		err = ctx.Err()
	}
	if err == nil {
		err = m.options.Bind(ctx, request.SandboxID, prepared)
	}
	if err == nil {
		m.satisfyPending(ctx, request.ID, prepared)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	e.cancel()
	e.cancel = nil
	if e.request.State == "cancelled" {
		m.changedLocked()
		go m.notify()
		return
	}
	e.request.State = "ready"
	if err != nil {
		e.request.State = "failed"
		e.request.Error = err.Error()
	}
	if m.closed {
		e.request.State = "interrupted"
		e.request.Error = "Preparation interrupted; explicit continue required"
	}
	if saveErr := m.saveLocked(e); saveErr != nil {
		e.request.State = "failed"
		e.request.Error = saveErr.Error()
	}
	m.changedLocked()
	go m.notify()
}

// Another owner-approved request may populate the same immutable bundle.
// Pending operations can use those verified bytes without another download.
func (m *Manager) satisfyPending(ctx context.Context, origin string, prepared sandbox.PreparedResources) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, e := range m.entries {
		if id == origin || e.request.BundleID != prepared.BundleID || e.request.State != "awaiting_consent" {
			continue
		}
		err := m.options.Bind(ctx, e.request.SandboxID, prepared)
		if err != nil {
			e.request.State = "failed"
			e.request.Error = err.Error()
		} else {
			e.request.State = "ready"
		}
		if err = m.saveLocked(e); err != nil {
			e.request.State = "failed"
			e.request.Error = err.Error()
		}
		m.changedLocked()
	}
}

func (m *Manager) Cancel(id string) (Request, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.entries[id]
	if e == nil {
		return Request{}, errors.New("resource request not found")
	}
	if e.request.State == "ready" {
		return e.request, nil
	}
	previous := e.request
	e.request.State = "cancelled"
	e.request.Error = "Cancelled by owner"
	if err := m.saveLocked(e); err != nil {
		e.request = previous
		return Request{}, err
	}
	if e.cancel != nil {
		e.cancel()
	}
	m.changedLocked()
	go m.notify()
	return e.request, nil
}
func (m *Manager) Close(ctx context.Context) error {
	m.mu.Lock()
	m.closed = true
	m.cancel()
	m.changedLocked()
	m.mu.Unlock()
	done := make(chan struct{})
	go func() { m.workers.Wait(); close(done) }()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return m.db.Close()
	}
}
