package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/pkg/storage"
)

type Registry struct {
	db      *sql.DB
	secrets *secrets.Store
}

func Open(path string, keys secrets.KeySource) (*Registry, error) {
	encrypted, err := secrets.Open(path, keys)
	if err != nil {
		return nil, err
	}
	db, err := storage.OpenSQLite(path)
	if err != nil {
		encrypted.Close()
		return nil, err
	}
	if _, err = db.Exec("CREATE TABLE IF NOT EXISTS providers(id TEXT PRIMARY KEY,metadata TEXT NOT NULL)"); err != nil {
		db.Close()
		encrypted.Close()
		return nil, err
	}
	return &Registry{db, encrypted}, nil
}
func (r *Registry) Close() error { return errors.Join(r.db.Close(), r.secrets.Close()) }
func (r *Registry) Get(ctx context.Context, id string) (Provider, error) {
	var data string
	err := r.db.QueryRowContext(ctx, "SELECT metadata FROM providers WHERE id=?", id).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return Provider{}, errors.New("provider unavailable; configure it on Control")
	}
	if err != nil {
		return Provider{}, err
	}
	var p Provider
	if err = json.Unmarshal([]byte(data), &p); err != nil {
		return Provider{}, err
	}
	return p, p.Validate()
}

// Save creates an immutable route; changed endpoints/credentials require a new
// identity so existing runs, receipts and budgets keep their original meaning.
func (r *Registry) Save(ctx context.Context, p Provider, key []byte) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if len(key) == 0 {
		return errors.New("provider credential missing")
	}
	for _, b := range key {
		if b < 32 || b == 127 {
			return errors.New("provider credential contains invalid header characters")
		}
	}
	// Canonical JSON of a struct is deterministic; slices retain explicit order.
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	// Validate metadata first; never mutate a secret if this ID has another route.
	var existing string
	err = r.db.QueryRowContext(ctx, "SELECT metadata FROM providers WHERE id=?", p.ID).Scan(&existing)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && existing != string(data) {
		return errors.New("provider reference already contains another configuration")
	}
	if err = r.secrets.Put(ctx, p.SecretRef, key); err != nil {
		return err
	}
	return storage.Transaction(ctx, r.db, func(tx *sql.Tx) error {
		var saved string
		err := tx.QueryRowContext(ctx, "SELECT metadata FROM providers WHERE id=?", p.ID).Scan(&saved)
		if err == nil {
			if saved != string(data) {
				return errors.New("provider reference already contains another configuration")
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO providers VALUES(?,?)", p.ID, string(data))
		return err
	})
}
func (r *Registry) Credential(ctx context.Context, p Provider) ([]byte, error) {
	saved, err := r.Get(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	expected, _ := json.Marshal(saved)
	supplied, _ := json.Marshal(p)
	if string(expected) != string(supplied) {
		return nil, errors.New("provider route differs from its registered configuration")
	}
	return r.secrets.Get(ctx, saved.SecretRef)
}
