package integrations

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jjyr/pot/pkg/storage"
)

type memorySecrets struct {
	values map[string][]byte
	reads  int
}

func (m *memorySecrets) Put(_ context.Context, id string, value []byte) error {
	m.values[id] = append([]byte(nil), value...)
	return nil
}
func (m *memorySecrets) Get(_ context.Context, id string) ([]byte, error) {
	m.reads++
	v, ok := m.values[id]
	if !ok {
		return nil, ErrNotFound
	}
	return append([]byte(nil), v...), nil
}
func (m *memorySecrets) Delete(_ context.Context, id string) error { delete(m.values, id); return nil }

type testAdapter struct {
	done         chan struct{}
	ignoreCancel bool
	refreshes    int
	expiry       *time.Time
}

func (a *testAdapter) Descriptor() Descriptor {
	return Descriptor{ID: "github", Name: "GitHub", LoginAvailable: true}
}
func (a *testAdapter) Begin(context.Context) (Challenge, error) {
	return Challenge{URL: "https://example.test/login", UserCode: "CODE-1234", ExpiresAt: time.Now().Add(time.Minute), Private: "private-device-code"}, nil
}
func (a *testAdapter) Authorize(ctx context.Context, _ Challenge) (Grant, error) {
	if a.ignoreCancel {
		<-a.done
	} else {
		select {
		case <-ctx.Done():
			return Grant{}, ctx.Err()
		case <-a.done:
		}
	}
	return Grant{Identity: Identity{ID: "123", Username: "fixture"}, Credential: Credential{AccessToken: "private-access-token", RefreshToken: "private-refresh-token", ExpiresAt: a.expiry}}, nil
}
func (a *testAdapter) Refresh(_ context.Context, previous Credential) (Credential, error) {
	a.refreshes++
	next := previous
	next.AccessToken = "refreshed-access-token"
	expiry := time.Now().Add(time.Hour)
	next.ExpiresAt = &expiry
	return next, nil
}
func fixture(t *testing.T) (*Service, *testAdapter, *memorySecrets, *sql.DB) {
	t.Helper()
	db, err := storage.OpenSQLite(filepath.Join(t.TempDir(), "shared.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	accounts, err := NewSQLAccounts(db)
	if err != nil {
		t.Fatal(err)
	}
	secrets := &memorySecrets{values: map[string][]byte{}}
	adapter := &testAdapter{done: make(chan struct{})}
	service, err := New(Options{Accounts: accounts, Secrets: secrets, Adapters: []Adapter{adapter}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { service.Close() })
	return service, adapter, secrets, db
}
func await(t *testing.T, s *Service, id string, want LoginState) {
	t.Helper()
	for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(time.Millisecond) {
		v, err := s.Login(context.Background(), "github", id)
		if err != nil {
			t.Fatal(err)
		}
		if v.Status == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("login %s, wanted %s", v.Status, want)
		}
	}
}
func connect(t *testing.T, s *Service, a *testAdapter) Login {
	t.Helper()
	v, err := s.BeginLogin(context.Background(), "github")
	if err != nil {
		t.Fatal(err)
	}
	close(a.done)
	await(t, s, v.ID, "succeeded")
	return v
}
func TestCatalogSingleAccountLogoutAndPendingFence(t *testing.T) {
	s, a, secrets, db := fixture(t)
	ctx := context.Background()
	items, err := s.List(ctx)
	if err != nil || len(items) != 1 || items[0].Status != "disconnected" {
		t.Fatal(items, err)
	}
	login, err := s.BeginLogin(ctx, "github")
	if err != nil {
		t.Fatal(err)
	}
	if has, _ := s.HasState(ctx); !has {
		t.Fatal("pending login does not hold ownership")
	}
	if _, err = s.BeginLogin(ctx, "github"); err == nil {
		t.Fatal("concurrent login accepted")
	}
	raw, _ := json.Marshal(login)
	if strings.Contains(string(raw), "private") {
		t.Fatal("private challenge exposed")
	}
	close(a.done)
	await(t, s, login.ID, "succeeded")
	if _, err = s.BeginLogin(ctx, "github"); err == nil {
		t.Fatal("second account accepted")
	}
	items, _ = s.List(ctx)
	raw, _ = json.Marshal(items)
	if strings.Contains(string(raw), "token") || strings.Contains(string(raw), "integration/") {
		t.Fatal("secret exposed", string(raw))
	}
	if items[0].Account.Identity.Username != "fixture" {
		t.Fatal(items)
	}
	rev := items[0].Account.Revision
	if err = s.Disconnect(ctx, "github", rev+1); !errors.Is(err, ErrChanged) {
		t.Fatal("stale logout accepted", err)
	}
	if err = s.Disconnect(ctx, "github", rev); err != nil {
		t.Fatal(err)
	}
	if len(secrets.values) != 0 {
		t.Fatal("logout left secret")
	}
	if _, err = s.Credential(ctx, "run", "github", false); err == nil {
		t.Fatal("logged-out credential readable")
	}
	// Re-login uses the tombstone revision, and an old logout cannot remove it.
	a.done = make(chan struct{})
	connect(t, s, a)
	if err = s.Disconnect(ctx, "github", rev); !errors.Is(err, ErrChanged) {
		t.Fatal("stale logout removed replacement", err)
	}
	var persisted string
	if err = db.QueryRow(`SELECT metadata FROM integration_accounts WHERE service='github'`).Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(persisted, "token") {
		t.Fatal("plaintext token in account store")
	}
}
func TestCancelRejectsLateAuthorizationAndShutsDown(t *testing.T) {
	s, a, _, _ := fixture(t)
	ctx := context.Background()
	a.ignoreCancel = true
	v, err := s.BeginLogin(ctx, "github")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CancelLogin(ctx, "github", v.ID); err != nil {
		t.Fatal(err)
	}
	close(a.done)
	await(t, s, v.ID, "cancelled")
	s.wg.Wait()
	items, _ := s.opts.Accounts.List(ctx)
	if len(items) != 0 {
		t.Fatal("late authorization rebound cancelled login")
	}
}
func TestExecutionCacheRefreshAndIsolation(t *testing.T) {
	s, a, secrets, _ := fixture(t)
	connect(t, s, a)
	ctx := context.Background()
	baseline := secrets.reads
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			token, err := s.Credential(ctx, "run", "github", false)
			if err != nil || token != "private-access-token" {
				t.Errorf("read %q %v", token, err)
			}
		}()
	}
	wg.Wait()
	if secrets.reads-baseline != 1 {
		t.Fatal("misses were not coalesced")
	}
	s.Forget("run")
	s.Credential(ctx, "run", "github", false)
	s.Credential(ctx, "other", "github", false)
	if secrets.reads-baseline != 3 {
		t.Fatal("scopes not isolated")
	}
	token, err := s.Credential(ctx, "run", "github", true)
	if err != nil || token != "refreshed-access-token" || a.refreshes != 1 {
		t.Fatal("forced refresh", err)
	}
	if len(secrets.values) != 1 {
		t.Fatal("old refresh token secret retained")
	}
	token, err = s.Credential(ctx, "other", "github", false)
	if err != nil || token != "refreshed-access-token" {
		t.Fatal("other scope kept stale credential")
	}
}
func TestExpiredCredentialAndNearExpiryRefresh(t *testing.T) {
	s, a, _, _ := fixture(t)
	now := time.Now()
	expiry := now.Add(time.Minute)
	a.expiry = &expiry
	connect(t, s, a)
	s.now = func() time.Time { return now }
	ctx := context.Background()
	s.Credential(ctx, "run", "github", false)
	s.now = func() time.Time { return now.Add(40 * time.Second) }
	if _, err := s.Credential(ctx, "run", "github", false); err != nil || a.refreshes != 1 {
		t.Fatal("near expiry refresh", err)
	}
	s.now = func() time.Time { return now.Add(2 * time.Hour) }
	items, _ := s.List(ctx)
	if items[0].Status != "expired" {
		t.Fatal("expired state missing")
	}
	if _, err := s.Credential(ctx, "run", "github", false); err == nil {
		t.Fatal("expired credential injected")
	}
}

type namedAdapter struct {
	*testAdapter
	id string
}

func (a namedAdapter) Descriptor() Descriptor {
	return Descriptor{ID: a.id, Name: a.id, LoginAvailable: true}
}
func TestAdditionalServiceUsesSameLifecycle(t *testing.T) {
	original, a, secrets, db := fixture(t)
	original.Close()
	store, err := NewSQLAccounts(db)
	if err != nil {
		t.Fatal(err)
	}
	b := &testAdapter{done: make(chan struct{})}
	s, err := New(Options{Accounts: store, Secrets: secrets, Adapters: []Adapter{a, namedAdapter{b, "issue-tracker"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	items, err := s.List(ctx)
	if err != nil || len(items) != 2 {
		t.Fatal(items, err)
	}
	one, err := s.BeginLogin(ctx, "github")
	if err != nil {
		t.Fatal(err)
	}
	two, err := s.BeginLogin(ctx, "issue-tracker")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CancelLogin(ctx, "issue-tracker", two.ID); err != nil {
		t.Fatal(err)
	}
	close(a.done)
	await(t, s, one.ID, "succeeded")
	items, _ = s.List(ctx)
	if items[0].Status != "connected" || items[1].Status != "disconnected" {
		t.Fatal("service states coupled", items)
	}
}

type startingAdapter struct{ started, stopped chan struct{} }

func (a startingAdapter) Descriptor() Descriptor {
	return Descriptor{ID: "github", Name: "GitHub", LoginAvailable: true}
}
func (a startingAdapter) Begin(ctx context.Context) (Challenge, error) {
	close(a.started)
	<-ctx.Done()
	close(a.stopped)
	return Challenge{}, ctx.Err()
}
func (a startingAdapter) Authorize(context.Context, Challenge) (Grant, error) {
	return Grant{}, errors.New("authorization must not start")
}
func TestCancellationDuringChallengeRequest(t *testing.T) {
	for _, closeService := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "shutdown"}[closeService], func(t *testing.T) {
			original, _, secrets, db := fixture(t)
			original.Close()
			accounts, _ := NewSQLAccounts(db)
			adapter := startingAdapter{make(chan struct{}), make(chan struct{})}
			s, err := New(Options{Accounts: accounts, Secrets: secrets, Adapters: []Adapter{adapter}})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			ctx := context.Background()
			finished := make(chan error, 1)
			go func() { _, err := s.BeginLogin(ctx, "github"); finished <- err }()
			<-adapter.started
			if closeService {
				s.Close()
			} else {
				items, err := s.List(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if err = s.CancelLogin(ctx, "github", items[0].Login.ID); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case <-adapter.stopped:
			case <-time.After(time.Second):
				t.Fatal("challenge request survived cancellation")
			}
			select {
			case err := <-finished:
				if err == nil {
					t.Fatal("cancelled challenge accepted")
				}
			case <-time.After(time.Second):
				t.Fatal("login request did not finish")
			}
		})
	}
}
