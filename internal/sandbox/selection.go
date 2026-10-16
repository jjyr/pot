package sandbox

import (
	"context"
	"database/sql"
	"errors"
)

func (m *Manager) Selected(ctx context.Context) (string, error) {
	if _, err := m.db.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS sandbox_settings(key TEXT PRIMARY KEY,value TEXT NOT NULL)"); err != nil {
		return "", err
	}
	var id string
	err := m.db.QueryRowContext(ctx, "SELECT value FROM sandbox_settings WHERE key='selected'").Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}
func (m *Manager) Select(ctx context.Context, id string) error {
	release, err := m.gate(ctx, "selection")
	if err != nil {
		return err
	}
	defer release()
	if _, err = m.Selected(ctx); err != nil {
		return err
	}
	definition, err := m.definition(ctx, id)
	if err != nil {
		return err
	}
	if definition.Retired || definition.Quarantined {
		return errors.New("select a non-retired reconciled Sandbox")
	}
	if m.providers[definition.Provider] == nil {
		return errors.New("selected Sandbox Provider unavailable")
	}
	_, err = m.db.ExecContext(ctx, "INSERT INTO sandbox_settings(key,value)VALUES('selected',?)ON CONFLICT(key)DO UPDATE SET value=excluded.value", id)
	return err
}
func (m *Manager) ResolveSelected(ctx context.Context) (Definition, error) {
	id, err := m.Selected(ctx)
	if err != nil {
		return Definition{}, err
	}
	if id == "" {
		definition, err := m.EnsureShared(ctx)
		if err != nil {
			return definition, err
		}
		if definition.Retired || definition.Quarantined {
			return definition, errors.New("default Sandbox unavailable; explicitly select a reconciled definition")
		}
		return definition, nil
	}
	definition, err := m.definition(ctx, id)
	if err != nil {
		return definition, err
	}
	if definition.Retired || definition.Quarantined {
		return definition, errors.New("selected Sandbox unavailable; no replacement chosen")
	}
	return definition, nil
}
