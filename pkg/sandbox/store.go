package sandbox

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/storage"
)

type Config struct {
	Root            string
	RuntimeID       string
	DB              *sql.DB
	Providers       []Provider
	DefaultProvider string
}
type Manager struct {
	root, runtimeID string
	db              *sql.DB
	ownsDB          bool
	mu              sync.Mutex
	providers       map[string]Provider
	defaultProvider string
	gates           map[string]chan struct{}
	closed          bool
	calls           map[string]map[string]context.CancelFunc
	liveLeases      map[string]string
	ownerLock       *os.File
	stopping        bool
	operations      sync.WaitGroup
	shutdownMu      sync.Mutex
}

func Open(ctx context.Context, config Config) (*Manager, error) {
	if !filepath.IsAbs(config.Root) || !core.Identifier(config.RuntimeID) {
		return nil, errors.New("Sandbox Manager requires absolute root and Runtime identity")
	}
	if err := os.MkdirAll(config.Root, 0700); err != nil {
		return nil, err
	}
	root, err := filepath.EvalSymlinks(config.Root)
	if err != nil {
		return nil, err
	}
	manager := &Manager{defaultProvider: config.DefaultProvider, root: root, runtimeID: config.RuntimeID, db: config.DB, providers: map[string]Provider{}, gates: map[string]chan struct{}{}, calls: map[string]map[string]context.CancelFunc{}, liveLeases: map[string]string{}}
	lock, err := os.OpenFile(filepath.Join(root, "sandbox-manager.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, errors.New("Sandbox Manager root already owned")
	}
	manager.ownerLock = lock
	if manager.db == nil {
		manager.db, err = storage.OpenSQLite(filepath.Join(root, "sandbox.sqlite"))
		if err != nil {
			manager.Close()
			return nil, err
		}
		manager.ownsDB = true
	}
	for _, provider := range config.Providers {
		descriptor := provider.Descriptor()
		if descriptor.ID == "" || manager.providers[descriptor.ID] != nil {
			manager.Close()
			return nil, errors.New("duplicate or empty Sandbox Provider")
		}
		manager.providers[descriptor.ID] = provider
	}
	err = storage.Transaction(ctx, manager.db, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS sandbox_definitions(id TEXT PRIMARY KEY,body TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS sandbox_scopes(id TEXT PRIMARY KEY,owner TEXT NOT NULL,target TEXT NOT NULL,body TEXT NOT NULL,state TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS sandbox_receipts(key TEXT PRIMARY KEY,target TEXT NOT NULL,owner TEXT NOT NULL,digest TEXT NOT NULL,operation_id TEXT NOT NULL,state TEXT NOT NULL,result BLOB);
CREATE TABLE IF NOT EXISTS sandbox_prepared_receipts(key TEXT PRIMARY KEY,input_hash TEXT NOT NULL,prepared_hash TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS sandbox_sessions(id TEXT PRIMARY KEY,scope_id TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS sandbox_leases(id TEXT PRIMARY KEY,target TEXT NOT NULL,owner TEXT NOT NULL,kind TEXT NOT NULL);`)
		if err != nil {
			return err
		}
		// An interrupted operation is never replayed after opening the ledger.
		if _, err = tx.ExecContext(ctx, "UPDATE sandbox_receipts SET state='unknown' WHERE state='started'"); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "UPDATE sandbox_scopes SET state='unknown' WHERE state!='closed'")
		return err
	})
	if err != nil {
		manager.Close()
		return nil, err
	}
	definitions, err := manager.Inventory(ctx)
	if err != nil {
		manager.Close()
		return nil, err
	}
	for _, definition := range definitions {
		if definition.Retired {
			continue
		}
		definition.ManagerEpoch++
		var pending int
		err = manager.db.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM sandbox_receipts WHERE target=? AND state='unknown')+(SELECT COUNT(*) FROM sandbox_scopes WHERE target=? AND state='unknown')+(SELECT COUNT(*) FROM sandbox_leases WHERE target=?)`, definition.ID, definition.ID, definition.ID).Scan(&pending)
		if err != nil {
			manager.Close()
			return nil, err
		}
		definition.Quarantined = definition.Quarantined || pending > 0
		if err := manager.saveDefinition(ctx, definition); err != nil {
			manager.Close()
			return nil, err
		}
	}
	return manager, nil
}
func (m *Manager) gate(ctx context.Context, key string) (func(), error) {
	m.mu.Lock()
	if m.closed || m.stopping {
		m.mu.Unlock()
		return nil, errors.New("Sandbox Manager closed")
	}
	gate := m.gates[key]
	if gate == nil {
		gate = make(chan struct{}, 1)
		gate <- struct{}{}
		m.gates[key] = gate
	}
	m.operations.Add(1)
	m.mu.Unlock()
	select {
	case <-ctx.Done():
		m.operations.Done()
		return nil, ctx.Err()
	case <-gate:
		return func() { gate <- struct{}{}; m.operations.Done() }, nil
	}
}
func (m *Manager) saveDefinition(ctx context.Context, definition Definition) error {
	return m.writeDefinition(ctx, definition, false)
}
func (m *Manager) writeDefinition(ctx context.Context, definition Definition, recovered bool) error {
	body, err := json.Marshal(definition)
	if err != nil {
		return err
	}
	// Quarantine is sticky across concurrent readiness/configuration snapshots.
	// Only explicit recovery, after provider proof and live-lease checks, clears it.
	_, err = m.db.ExecContext(ctx, `INSERT INTO sandbox_definitions(id,body) VALUES (?,?) ON CONFLICT(id) DO UPDATE SET body=CASE WHEN ?=0 AND json_extract(sandbox_definitions.body,'$.quarantined') THEN json_set(excluded.body,'$.quarantined',json('true')) ELSE excluded.body END`, definition.ID, string(body), recovered)
	return err
}
func (m *Manager) definition(ctx context.Context, id string) (Definition, error) {
	var body []byte
	var definition Definition
	if !validID(id) {
		return definition, errors.New("invalid Sandbox ID")
	}
	if err := m.db.QueryRowContext(ctx, "SELECT body FROM sandbox_definitions WHERE id=?", id).Scan(&body); err != nil {
		return definition, err
	}
	if err := json.Unmarshal(body, &definition); err != nil {
		return definition, err
	}
	if definition.Retired {
		return definition, errors.New("Sandbox ID retired")
	}
	return definition, nil
}
func (m *Manager) provider(ctx context.Context, id string) (Definition, Provider, Instance, error) {
	definition, err := m.definition(ctx, id)
	if err != nil {
		return definition, nil, Instance{}, err
	}
	provider := m.providers[definition.Provider]
	if provider == nil {
		return definition, nil, Instance{}, fmt.Errorf("Sandbox Provider %s is not installed", definition.Provider)
	}
	return definition, provider, Instance{Root: m.root, ID: id, Resources: definition.Resources, Mappings: definition.Mappings}, nil
}
func (m *Manager) Inventory(ctx context.Context) ([]Definition, error) {
	rows, err := m.db.QueryContext(ctx, "SELECT body FROM sandbox_definitions ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Definition{}
	for rows.Next() {
		var body []byte
		var value Definition
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(body, &value); err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, rows.Err()
}
func (m *Manager) Create(ctx context.Context, id, name, providerID string, resources Resources) (Definition, error) {
	if !validID(id) || name == "" || len(name) > 256 {
		return Definition{}, errors.New("invalid Sandbox definition")
	}
	release, err := m.gate(ctx, "instance:"+id)
	if err != nil {
		return Definition{}, err
	}
	defer release()
	provider := m.providers[providerID]
	if provider == nil {
		return Definition{}, errors.New("Sandbox Provider not installed")
	}
	if resources == (Resources{}) {
		resources = provider.DefaultResources()
	}
	instance := Instance{Root: m.root, ID: id, Resources: resources}
	if err := provider.ValidateResources(instance, resources); err != nil {
		return Definition{}, err
	}
	if previous, lookupErr := m.definition(ctx, id); lookupErr == nil {
		if previous.Retired || previous.Name != name || previous.Provider != providerID || previous.Resources != resources {
			return Definition{}, errors.New("Sandbox ID already has another immutable definition")
		}
		if err := os.MkdirAll(provider.Workspace(instance), 0700); err != nil {
			return previous, err
		}
		return previous, nil
	} else if !errors.Is(lookupErr, sql.ErrNoRows) {
		return Definition{}, lookupErr
	}
	definition := Definition{ID: id, Name: name, Provider: providerID, Resources: resources, ManagerEpoch: 1}
	body, _ := json.Marshal(definition)
	if _, err := m.db.ExecContext(ctx, "INSERT INTO sandbox_definitions(id,body) VALUES (?,?)", id, string(body)); err != nil {
		return Definition{}, err
	}
	if err := os.MkdirAll(provider.Workspace(instance), 0700); err != nil {
		return definition, err
	}
	return definition, nil
}
func (m *Manager) EnsureShared(ctx context.Context) (Definition, error) {
	if definition, err := m.definition(ctx, SharedID); err == nil {
		return definition, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return Definition{}, err
	}
	provider := m.defaultProvider
	if provider == "" {
		return Definition{}, errors.New("no default Sandbox Provider configured")
	}
	definition, err := m.Create(ctx, SharedID, "Shared development environment", provider, Resources{})
	if err != nil {
		if existing, lookup := m.definition(ctx, SharedID); lookup == nil {
			return existing, nil
		}
	}
	return definition, err
}
func (m *Manager) Workspace(ctx context.Context, id string) (string, error) {
	_, provider, instance, err := m.provider(ctx, id)
	if err != nil {
		return "", err
	}
	return provider.Workspace(instance), nil
}
func (m *Manager) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	m.mu.Unlock()
	var result error
	if m.ownsDB && m.db != nil {
		result = m.db.Close()
	}
	if m.ownerLock != nil {
		result = errors.Join(result, syscall.Flock(int(m.ownerLock.Fd()), syscall.LOCK_UN), m.ownerLock.Close())
	}
	return result
}

// Descriptors lists installed capabilities without starting helpers or guests.
func (m *Manager) Descriptors() []Descriptor {
	out := []Descriptor{}
	for _, provider := range m.providers {
		out = append(out, provider.Descriptor())
	}
	return out
}
