// Package credentials owns external-service accounts and execution-scoped,
// memory-only credential caches. Configuring an account makes it available to
// all assistants and tasks of its Control; model-provider secrets stay separate.
package credentials

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/storage"
)

type Account struct {
	Service   string     `json:"service"`
	Account   string     `json:"account"`
	Name      string     `json:"name"`
	Default   bool       `json:"default"`
	Revision  uint64     `json:"revision"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}
type SaveRequest struct {
	Service          string     `json:"service"`
	Account          string     `json:"account"`
	Name             string     `json:"name"`
	Default          bool       `json:"default"`
	ExpectedRevision uint64     `json:"expected_revision"`
	Token            string     `json:"token"`
	ExpiresAt        *time.Time `json:"expires_at,omitempty"`
}
type RemoveRequest struct {
	Service          string `json:"service"`
	Account          string `json:"account"`
	ExpectedRevision uint64 `json:"expected_revision"`
}
type cached struct {
	value   string
	expires *time.Time
	loaded  time.Time
}
type Store struct {
	mu      sync.Mutex
	db      *sql.DB
	secrets *secrets.Store
	scopes  map[string]map[string]cached
	now     func() time.Time
}

func Open(path string, keys secrets.KeySource) (*Store, error) {
	encrypted, err := secrets.Open(path, keys)
	if err != nil {
		return nil, err
	}
	db, err := storage.OpenSQLite(path)
	if err != nil {
		encrypted.Close()
		return nil, err
	}
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS credential_accounts(service TEXT NOT NULL, account TEXT NOT NULL, metadata TEXT NOT NULL, secret_ref TEXT NOT NULL, PRIMARY KEY(service,account))`)
	if err != nil {
		db.Close()
		encrypted.Close()
		return nil, err
	}
	return &Store{db: db, secrets: encrypted, scopes: map[string]map[string]cached{}, now: time.Now}, nil
}
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	clear(s.scopes)
	return errors.Join(s.db.Close(), s.secrets.Close())
}
func (s *Store) List(ctx context.Context) ([]Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.list(ctx)
}
func (s *Store) list(ctx context.Context) ([]Account, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT metadata FROM credential_accounts ORDER BY service,account")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Account{}
	for rows.Next() {
		var raw string
		var a Account
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &a); err != nil {
			return nil, err
		}
		items = append(items, a)
	}
	return items, rows.Err()
}
func (s *Store) Save(ctx context.Context, r SaveRequest) (Account, error) {
	if err := (core.CredentialRef{Service: r.Service, Account: r.Account}).Validate(); err != nil {
		return Account{}, err
	}
	if r.Account == "" || strings.TrimSpace(r.Name) == "" || len(r.Name) > 256 || len(r.Token) > 65536 || strings.ContainsAny(r.Token, "\x00\r\n") {
		return Account{}, errors.New("invalid account name or token")
	}
	if r.ExpiresAt != nil && !r.ExpiresAt.After(s.now()) {
		return Account{}, errors.New("credential is already expired")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	items, err := s.list(ctx)
	if err != nil {
		return Account{}, err
	}
	var previous Account
	var previousRef string
	hasDefault := false
	for _, a := range items {
		if a.Service == r.Service {
			hasDefault = hasDefault || a.Default
			if a.Account == r.Account {
				previous = a
			}
		}
	}
	if previous.Revision != r.ExpectedRevision {
		return Account{}, errors.New("account changed; refresh before saving")
	}
	if previous.Revision > 0 {
		if err = s.db.QueryRowContext(ctx, "SELECT secret_ref FROM credential_accounts WHERE service=? AND account=?", r.Service, r.Account).Scan(&previousRef); err != nil {
			return Account{}, err
		}
	}
	ref := previousRef
	if r.Token != "" {
		ref = "connection/" + storage.ID()
		if err = s.secrets.Put(ctx, ref, []byte(r.Token)); err != nil {
			return Account{}, err
		}
	} else if ref == "" {
		return Account{}, errors.New("account token is required")
	}
	a := Account{Service: r.Service, Account: r.Account, Name: r.Name, Default: r.Default || !hasDefault || previous.Default, Revision: previous.Revision + 1, ExpiresAt: r.ExpiresAt}
	err = storage.Transaction(ctx, s.db, func(tx *sql.Tx) error {
		if a.Default {
			for _, other := range items {
				if other.Service == a.Service && other.Account != a.Account && other.Default {
					other.Default = false
					other.Revision++
					raw, _ := json.Marshal(other)
					if _, err := tx.ExecContext(ctx, "UPDATE credential_accounts SET metadata=? WHERE service=? AND account=?", string(raw), other.Service, other.Account); err != nil {
						return err
					}
				}
			}
		}
		raw, _ := json.Marshal(a)
		_, err := tx.ExecContext(ctx, "INSERT INTO credential_accounts VALUES(?,?,?,?) ON CONFLICT(service,account) DO UPDATE SET metadata=excluded.metadata,secret_ref=excluded.secret_ref", a.Service, a.Account, string(raw), ref)
		return err
	})
	if err != nil {
		return Account{}, err
	}
	clear(s.scopes)
	return a, nil
}
func (s *Store) Remove(ctx context.Context, r RemoveRequest) error {
	if err := (core.CredentialRef{Service: r.Service, Account: r.Account}).Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	items, err := s.list(ctx)
	if err != nil {
		return err
	}
	var found *Account
	for i := range items {
		if items[i].Service == r.Service && items[i].Account == r.Account {
			found = &items[i]
		}
	}
	if found == nil || found.Revision != r.ExpectedRevision {
		return errors.New("account changed; refresh before deleting")
	}
	err = storage.Transaction(ctx, s.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "DELETE FROM credential_accounts WHERE service=? AND account=?", r.Service, r.Account); err != nil {
			return err
		}
		if found.Default {
			for _, a := range items {
				if a.Service == r.Service && a.Account != r.Account {
					a.Default = true
					a.Revision++
					raw, _ := json.Marshal(a)
					_, err := tx.ExecContext(ctx, "UPDATE credential_accounts SET metadata=? WHERE service=? AND account=?", string(raw), a.Service, a.Account)
					return err
				}
			}
		}
		return nil
	})
	if err == nil {
		clear(s.scopes)
	}
	return err
}

// Environment reads each value once per scope, coalescing concurrent misses.
// Expiring values are reread near expiry. Static PATs cannot be renewed here;
// expired values fail clearly until the owner updates the account.
func (s *Store) Environment(ctx context.Context, scope string, bindings core.CredentialBindings, refresh bool) (map[string]string, error) {
	if scope == "" {
		return nil, errors.New("credential execution scope is required")
	}
	if err := bindings.Validate(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cache := s.scopes[scope]
	if cache == nil {
		cache = map[string]cached{}
		s.scopes[scope] = cache
	}
	result := map[string]string{}
	if refresh {
		for _, ref := range bindings {
			delete(cache, ref.Service+"/"+ref.Account)
		}
	}
	for env, ref := range bindings {
		key := ref.Service + "/" + ref.Account
		now := s.now()
		value, ok := cache[key]
		if !ok || value.expires != nil && !now.Add(30*time.Second).Before(*value.expires) && now.Sub(value.loaded) >= time.Second {
			items, err := s.list(ctx)
			if err != nil {
				return nil, err
			}
			var selected *Account
			for i := range items {
				a := &items[i]
				if a.Service == ref.Service && (ref.Account != "" && a.Account == ref.Account || ref.Account == "" && a.Default) {
					selected = a
					break
				}
			}
			if selected == nil {
				return nil, errors.New("service account is not configured on Control: " + key)
			}
			if selected.ExpiresAt != nil && !selected.ExpiresAt.After(now) {
				delete(cache, key)
				return nil, errors.New("service credential expired; update the account on Control: " + key)
			}
			var secretRef string
			if err := s.db.QueryRowContext(ctx, "SELECT secret_ref FROM credential_accounts WHERE service=? AND account=?", selected.Service, selected.Account).Scan(&secretRef); err != nil {
				return nil, err
			}
			raw, err := s.secrets.Get(ctx, secretRef)
			if err != nil {
				return nil, err
			}
			value = cached{value: string(raw), expires: selected.ExpiresAt, loaded: now}
			clear(raw)
			cache[key] = value
		}
		if value.expires != nil && !value.expires.After(now) {
			delete(cache, key)
			return nil, errors.New("service credential expired")
		}
		result[env] = value.value
	}
	return result, nil
}
func (s *Store) Forget(scope string) { s.mu.Lock(); defer s.mu.Unlock(); delete(s.scopes, scope) }
