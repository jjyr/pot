package resourcecontrol

import (
	"context"
	"errors"
	"sort"
)

type CacheItem struct {
	ID         string   `json:"id"`
	Filename   string   `json:"filename"`
	SHA256     string   `json:"sha256"`
	Present    bool     `json:"present"`
	DiskBytes  uint64   `json:"disk_bytes"`
	References []string `json:"references"`
	Prunable   bool     `json:"prunable"`
}
type CacheStatus struct {
	Preparing bool        `json:"preparing"`
	Items     []CacheItem `json:"items"`
}

func (m *Manager) referencesLocked(resourceID string) []string {
	refs := map[string]bool{}
	digest := m.options.Catalog.Resources[resourceID].SHA256
	for _, e := range m.entries {
		b := m.options.Catalog.Bundles[e.request.BundleID]
		for _, id := range b.ResourceIDs() {
			if m.options.Catalog.Resources[id].SHA256 == digest {
				refs[e.request.SandboxID] = true
			}
		}
	}
	out := []string{}
	for id := range refs {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
func (m *Manager) preparingLocked() bool {
	for _, e := range m.entries {
		if e.cancel != nil {
			return true
		}
	}
	return false
}
func (m *Manager) cache(ctx context.Context) (CacheStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := CacheStatus{Preparing: m.preparingLocked(), Items: []CacheItem{}}
	for id, r := range m.options.Catalog.Resources {
		present, size, err := m.options.Store.DiskUsage(ctx, r)
		if err != nil {
			return out, err
		}
		refs := m.referencesLocked(id)
		out.Items = append(out.Items, CacheItem{ID: id, Filename: r.Filename, SHA256: r.SHA256, Present: present, DiskBytes: size, References: refs, Prunable: present && len(refs) == 0 && !out.Preparing})
	}
	sort.Slice(out.Items, func(i, j int) bool { return out.Items[i].ID < out.Items[j].ID })
	return out, nil
}
func (m *Manager) Prune(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errors.New("resource manager closed")
	}
	if m.preparingLocked() {
		return errors.New("resources are being prepared")
	}
	r, ok := m.options.Catalog.Resources[id]
	if !ok {
		return errors.New("unknown resource")
	}
	if len(m.referencesLocked(id)) > 0 {
		return errors.New("resource is referenced by a Sandbox")
	}
	if err := m.options.Store.Prune(ctx, r); err != nil {
		return err
	}
	m.changedLocked()
	go m.notify()
	return nil
}

// ForgetAfter serializes a definition's confirmed deletion with request pins.
// Failed deletion preserves references; active preparation must settle first.
func (m *Manager) ForgetAfter(ctx context.Context, id string, remove func() error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errors.New("resource manager closed")
	}
	for _, e := range m.entries {
		if e.request.SandboxID == id && e.cancel != nil {
			return errors.New("Sandbox resources are being prepared")
		}
	}
	if err := remove(); err != nil {
		return err
	}
	if _, err := m.db.ExecContext(ctx, "DELETE FROM resource_requests WHERE sandbox_id=?", id); err != nil {
		return err
	}
	for key, e := range m.entries {
		if e.request.SandboxID == id {
			delete(m.entries, key)
		}
	}
	m.changedLocked()
	go m.notify()
	return nil
}
