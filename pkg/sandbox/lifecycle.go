package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/storage"
)

type Lease struct {
	manager  *Manager
	id       string
	mu       sync.Mutex
	released bool
}

func (l *Lease) Release(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.released {
		return nil
	}
	if _, err := l.manager.db.ExecContext(ctx, "DELETE FROM sandbox_leases WHERE id=?", l.id); err != nil {
		return err
	}
	l.manager.mu.Lock()
	delete(l.manager.liveLeases, l.id)
	l.manager.mu.Unlock()
	l.released = true
	return nil
}
func (m *Manager) Acquire(ctx context.Context, id, owner string) (*Lease, error) {
	release, err := m.gate(ctx, "instance:"+id)
	if err != nil {
		return nil, err
	}
	defer release()
	definition, err := m.definition(ctx, id)
	if err != nil {
		return nil, err
	}
	if definition.Quarantined {
		return nil, errors.Join(core.ErrCleanupUnconfirmed, errors.New("Sandbox is quarantined"))
	}
	if provider := m.providers[definition.Provider]; provider != nil && !provider.Descriptor().Capabilities.SharedExecution {
		var count int
		if err := m.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sandbox_leases WHERE target=? AND owner!=?", id, owner).Scan(&count); err != nil {
			return nil, err
		}
		if count > 0 {
			return nil, errors.New("Sandbox requires one exclusive execution owner")
		}
	}

	lease := &Lease{manager: m, id: storage.ID()}
	_, err = m.db.ExecContext(ctx, "INSERT INTO sandbox_leases VALUES (?,?,?,'run')", lease.id, id, owner)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.liveLeases[lease.id] = id
	m.mu.Unlock()
	return lease, nil
}
func (m *Manager) occupied(ctx context.Context, id string) error {
	var count int
	if err := m.db.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM sandbox_leases WHERE target=?)+(SELECT COUNT(*) FROM sandbox_receipts WHERE target=? AND state IN ('started','unknown'))`, id, id).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return errors.New("Sandbox has active leases or uncertain operations")
	}
	return nil
}
func (m *Manager) BindResources(ctx context.Context, id string, resources PreparedResources) error {
	release, err := m.gate(ctx, "instance:"+id)
	if err != nil {
		return err
	}
	defer release()
	definition, provider, instance, err := m.provider(ctx, id)
	if err != nil {
		return err
	}
	if definition.Quarantined {
		return core.ErrCleanupUnconfirmed
	}
	if err := m.occupied(ctx, id); err != nil {
		return err
	}
	state, err := provider.Lifecycle(ctx, instance, "status")
	if err != nil {
		return err
	}
	if state.Phase != "stopped" && state.Phase != "unavailable" {
		return errors.New("stop Sandbox before binding resources")
	}
	return provider.BindResources(ctx, instance, resources)
}
func (m *Manager) Lifecycle(ctx context.Context, id, action string) (out Status, result error) {
	defer func() {
		if errors.Is(result, core.ErrCleanupUnconfirmed) {
			result = m.quarantine(context.WithoutCancel(ctx), id, result)
		}
	}()
	switch action {
	case "status", "prepare", "start", "stop", "delete", "rebuild":
	default:
		return Status{}, errors.New("unsupported Sandbox lifecycle operation")
	}
	release, err := m.gate(ctx, "instance:"+id)
	if err != nil {
		return Status{}, err
	}
	defer release()
	definition, provider, instance, err := m.provider(ctx, id)
	if err != nil {
		return Status{}, err
	}
	if action != "status" {
		if definition.Quarantined {
			return Status{}, errors.Join(core.ErrCleanupUnconfirmed, errors.New("recover quarantined Sandbox explicitly"))
		}
		if err := m.occupied(ctx, id); err != nil {
			return Status{}, err
		}
	}
	if action == "rebuild" {
		state, err := provider.Lifecycle(ctx, instance, "delete")
		if err != nil {
			return state, err
		}
		if state.Phase != "stopped" {
			return state, core.ErrCleanupUnconfirmed
		}
		workspace, err := os.OpenRoot(filepath.Dir(provider.Workspace(instance)))
		if err != nil {
			return state, err
		}
		err = workspace.RemoveAll("development")
		closeErr := workspace.Close()
		if err != nil || closeErr != nil {
			return state, errors.Join(err, closeErr)
		}
		definition.ManagerEpoch++
		definition.GuestGeneration = nil
		if err := m.saveDefinition(ctx, definition); err != nil {
			return state, err
		}
		return state, nil
	}
	state, err := provider.Lifecycle(ctx, instance, action)
	if err != nil {
		return state, err
	}
	if action == "delete" {
		if state.Phase != "stopped" {
			return state, core.ErrCleanupUnconfirmed
		}
		definition.Retired = true
		definition.ManagerEpoch++
		if err := m.saveDefinition(ctx, definition); err != nil {
			return state, err
		}
	}
	return state, nil
}
func (m *Manager) Configure(ctx context.Context, id string, resources Resources) error {
	release, err := m.gate(ctx, "instance:"+id)
	if err != nil {
		return err
	}
	defer release()
	definition, provider, instance, err := m.provider(ctx, id)
	if err != nil {
		return err
	}
	if definition.Quarantined {
		return core.ErrCleanupUnconfirmed
	}
	if err := m.occupied(ctx, id); err != nil {
		return err
	}
	state, err := provider.Lifecycle(ctx, instance, "status")
	if err != nil {
		return err
	}
	if state.Phase != "stopped" {
		return errors.New("stop Sandbox before configuring resources")
	}
	if err := provider.ValidateResources(instance, resources); err != nil {
		return err
	}
	definition.Resources = resources
	return m.saveDefinition(ctx, definition)
}
func (m *Manager) ready(ctx context.Context, id string) (Definition, Provider, Instance, Status, error) {
	definition, provider, instance, state, err := m.readyUnchecked(ctx, id)
	if errors.Is(err, core.ErrCleanupUnconfirmed) {
		err = m.quarantine(context.WithoutCancel(ctx), id, err)
	}
	return definition, provider, instance, state, err
}
func (m *Manager) readyUnchecked(ctx context.Context, id string) (Definition, Provider, Instance, Status, error) {
	release, err := m.gate(ctx, "instance:"+id)
	if err != nil {
		return Definition{}, nil, Instance{}, Status{}, err
	}
	defer release()
	definition, provider, instance, err := m.provider(ctx, id)
	if err != nil {
		return definition, provider, instance, Status{}, err
	}
	if definition.Quarantined {
		return definition, provider, instance, Status{}, errors.Join(core.ErrCleanupUnconfirmed, errors.New("Sandbox requires explicit recovery"))
	}
	state, err := provider.Lifecycle(ctx, instance, "status")
	if err != nil {
		return definition, provider, instance, state, err
	}
	if state.Phase == "stopped" {
		state, err = provider.Lifecycle(ctx, instance, "start")
		if err != nil {
			return definition, provider, instance, state, err
		}
	}
	deadline := time.NewTimer(60 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if state.Phase == "running" {
			healthy, err := provider.Health(ctx, instance)
			if err == nil && healthy {
				break
			}
		}
		if state.Phase == "failed" || state.Phase == "unavailable" {
			return definition, provider, instance, state, fmt.Errorf("Sandbox is %s", state.Phase)
		}
		select {
		case <-ctx.Done():
			return definition, provider, instance, state, ctx.Err()
		case <-deadline.C:
			return definition, provider, instance, state, errors.New("authenticated guest executor did not become ready")
		case <-ticker.C:
			state, err = provider.Lifecycle(ctx, instance, "status")
			if err != nil {
				return definition, provider, instance, state, err
			}
		}
	}
	if definition.GuestGeneration == nil || *definition.GuestGeneration != state.Generation {
		if definition.GuestGeneration != nil {
			definition.ManagerEpoch++
		}
		generation := state.Generation
		definition.GuestGeneration = &generation
		if err := m.saveDefinition(ctx, definition); err != nil {
			return definition, provider, instance, state, err
		}
	}
	return definition, provider, instance, state, nil
}

// Recover is an explicit owner action. It confirms closure of recorded scopes
// through the original provider policy or a stopped/new VM generation. Unknown
// command receipts remain unknown and are never replayed by this operation.
func (m *Manager) Recover(ctx context.Context, id string) error {
	release, err := m.gate(ctx, "instance:"+id)
	if err != nil {
		return err
	}
	defer release()
	definition, provider, instance, err := m.provider(ctx, id)
	if err != nil {
		return err
	}
	if !definition.Quarantined {
		return errors.New("Sandbox does not require recovery")
	}
	m.mu.Lock()
	activeLease := false
	for _, target := range m.liveLeases {
		if target == id {
			activeLease = true
		}
	}
	m.mu.Unlock()
	if activeLease {
		return errors.New("Sandbox still has live Runtime leases")
	}
	state, err := provider.Lifecycle(ctx, instance, "status")
	if err != nil {
		return err
	}
	scopes, err := m.scopes(ctx, "target=? AND state!='closed'", id)
	if err != nil {
		return err
	}
	for _, scope := range scopes {
		proven := state.Phase == "stopped" || (state.Phase == "running" && state.Generation != scope.ProviderGeneration)
		if !proven {
			body, _ := json.Marshal(map[string]any{"id": scope.ID})
			response, err := provider.Request(ctx, instance, "finish", body, 15*time.Second)
			if err != nil {
				return errors.Join(core.ErrCleanupUnconfirmed, err)
			}
			var result map[string]any
			if json.Unmarshal(response, &result) != nil || !provider.CleanupPolicy().Confirmed(result) {
				return core.ErrCleanupUnconfirmed
			}
		}
		if err := m.closeScope(ctx, scope); err != nil {
			return err
		}
	}
	// Durable run leases were owned by the previous interrupted Runtime. Active
	// operations are serialized by instance/owner gates before owner recovery.
	var active int
	if err := m.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sandbox_receipts WHERE target=? AND state='started'", id).Scan(&active); err != nil {
		return err
	}
	if active != 0 {
		return errors.New("Sandbox operation still active")
	}
	if _, err := m.db.ExecContext(ctx, "DELETE FROM sandbox_leases WHERE target=?", id); err != nil {
		return err
	}
	if _, err := m.db.ExecContext(ctx, "UPDATE sandbox_receipts SET state='acknowledged_unknown' WHERE target=? AND state='unknown'", id); err != nil {
		return err
	}
	definition.Quarantined = false
	definition.ManagerEpoch++
	generation := state.Generation
	definition.GuestGeneration = &generation
	return m.writeDefinition(ctx, definition, true)
}
func (m *Manager) Shutdown(ctx context.Context) error {
	m.shutdownMu.Lock()
	defer m.shutdownMu.Unlock()
	m.mu.Lock()
	m.stopping = true
	for _, calls := range m.calls {
		for _, cancel := range calls {
			cancel()
		}
	}
	m.mu.Unlock()
	settled := make(chan struct{})
	go func() { m.operations.Wait(); close(settled) }()
	select {
	case <-ctx.Done():
		return errors.Join(core.ErrCleanupUnconfirmed, ctx.Err())
	case <-settled:
	}
	scopes, err := m.scopes(ctx, "state!='closed'")
	if err != nil {
		return err
	}
	var result error
	for _, scope := range scopes {
		result = errors.Join(result, m.cancelScope(ctx, scope))
	}
	for _, provider := range m.providers {
		if err := provider.Shutdown(ctx); err != nil {
			result = errors.Join(result, core.ErrCleanupUnconfirmed, err)
		}
	}
	return result
}
func (m *Manager) quarantine(ctx context.Context, id string, cause error) error {
	_, err := m.db.ExecContext(ctx, `UPDATE sandbox_definitions SET body=json_set(body,'$.quarantined',json('true')) WHERE id=?`, id)
	return errors.Join(core.ErrCleanupUnconfirmed, cause, err)
}
func (m *Manager) GuestWorkspace(ctx context.Context, id string) (string, error) {
	_, provider, _, err := m.provider(ctx, id)
	if err != nil {
		return "", err
	}
	return provider.GuestWorkspace(), nil
}
func guestCWD(provider Provider, grant AccessGrant) string {
	cwd := provider.GuestWorkspace()
	if grant.Worker {
		cwd = filepath.ToSlash(filepath.Join(cwd, "tasks", grant.WorkspaceOwner))
	}
	return cwd
}

// ConfigureMappings is an owner operation; model tool arguments cannot install
// Host mounts. Mappings are revalidated immediately before each provider boot.
func (m *Manager) ConfigureMappings(ctx context.Context, id string, mappings []HostMapping) error {
	if err := ValidateMappings(mappings); err != nil {
		return err
	}
	release, err := m.gate(ctx, "instance:"+id)
	if err != nil {
		return err
	}
	defer release()
	definition, provider, instance, err := m.provider(ctx, id)
	if err != nil {
		return err
	}
	if definition.Provider == AppleVM {
		return errors.New("enrolled macOS VM does not support arbitrary Host mappings")
	}
	if definition.Quarantined {
		return core.ErrCleanupUnconfirmed
	}
	if err := m.occupied(ctx, id); err != nil {
		return err
	}
	state, err := provider.Lifecycle(ctx, instance, "status")
	if err != nil {
		return err
	}
	if state.Phase != "stopped" {
		return errors.New("stop Sandbox before changing Host mappings")
	}
	definition.Mappings = append([]HostMapping{}, mappings...)
	return m.saveDefinition(ctx, definition)
}
