package credentials

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/pkg/core"
)

type countedKey struct {
	secrets.KeySource
	reads atomic.Int64
}

func (k *countedKey) Key(ctx context.Context, create bool) ([]byte, error) {
	k.reads.Add(1)
	return k.KeySource.Key(ctx, create)
}
func testStore(t *testing.T) (*Store, *countedKey, string) {
	t.Helper()
	key, _ := secrets.NewMemoryKey(bytes.Repeat([]byte{7}, 32))
	counted := &countedKey{KeySource: key}
	path := filepath.Join(t.TempDir(), "control.sqlite")
	s, err := Open(path, counted)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, counted, path
}
func saveAccount(t *testing.T, s *Store, r SaveRequest) Account {
	t.Helper()
	a, err := s.Save(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	return a
}
func TestScopeCachingConcurrentReadsRotationAndRemoval(t *testing.T) {
	s, key, path := testStore(t)
	ctx := context.Background()
	a := saveAccount(t, s, SaveRequest{Service: "github", Account: "personal", Name: "Personal", Token: "fixture-credential-never-in-storage"})
	bindings := core.CredentialBindings{"GH_TOKEN": {Service: "github"}}
	baseline := key.reads.Load()
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			env, err := s.Environment(ctx, "run", bindings, false)
			if err != nil || env["GH_TOKEN"] != "fixture-credential-never-in-storage" {
				t.Errorf("read: %v", err)
			}
		}()
	}
	wg.Wait()
	if got := key.reads.Load() - baseline; got != 1 {
		t.Fatalf("concurrent misses decrypted %d times", got)
	}
	if _, err := s.Environment(ctx, "other", bindings, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Environment(ctx, "run", bindings, true); err != nil {
		t.Fatal(err)
	}
	if got := key.reads.Load() - baseline; got != 3 {
		t.Fatalf("scope/refresh reads: %d", got)
	}
	s.Forget("run")
	if _, err := s.Environment(ctx, "run", bindings, false); err != nil {
		t.Fatal(err)
	}
	if key.reads.Load()-baseline != 4 {
		t.Fatal("scope cleanup did not clear cached value")
	}
	a = saveAccount(t, s, SaveRequest{Service: a.Service, Account: a.Account, Name: a.Name, ExpectedRevision: a.Revision, Token: "rotated-fixture-credential"})
	for _, scope := range []string{"run", "other"} {
		env, err := s.Environment(ctx, scope, bindings, false)
		if err != nil || env["GH_TOKEN"] != "rotated-fixture-credential" {
			t.Fatal("rotation left stale cache", err)
		}
	}
	if err := s.Remove(ctx, RemoveRequest{Service: a.Service, Account: a.Account, ExpectedRevision: a.Revision}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Environment(ctx, "run", bindings, false); err == nil {
		t.Fatal("deleted credential still readable")
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		raw, _ := os.ReadFile(path + suffix)
		if bytes.Contains(raw, []byte("fixture-credential-never-in-storage")) || bytes.Contains(raw, []byte("rotated-fixture-credential")) {
			t.Fatal("plaintext credential persisted")
		}
	}
}
func TestDefaultsExpiryAndPublicMetadata(t *testing.T) {
	s, key, _ := testStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	s.now = func() time.Time { return now }
	expiry := now.Add(time.Minute)
	first := saveAccount(t, s, SaveRequest{Service: "github", Account: "first", Name: "First", Token: "first-secret"})
	second := saveAccount(t, s, SaveRequest{Service: "github", Account: "second", Name: "Second", Default: true, Token: "second-secret", ExpiresAt: &expiry})
	bindings := core.CredentialBindings{"GH_TOKEN": {Service: "github"}}
	env, err := s.Environment(ctx, "run", bindings, false)
	if err != nil || env["GH_TOKEN"] != "second-secret" {
		t.Fatal("default selection", err)
	}
	baseline := key.reads.Load()
	now = now.Add(40 * time.Second)
	if _, err = s.Environment(ctx, "run", bindings, false); err != nil {
		t.Fatal(err)
	}
	if key.reads.Load() != baseline+1 {
		t.Fatal("near-expiry cache was not refreshed")
	}
	now = now.Add(21 * time.Second)
	if _, err = s.Environment(ctx, "run", bindings, false); err == nil {
		t.Fatal("expired cached token accepted")
	}
	explicit := core.CredentialBindings{"TOKEN": {Service: "github", Account: first.Account}}
	env, err = s.Environment(ctx, "run", explicit, false)
	if err != nil || env["TOKEN"] != "first-secret" {
		t.Fatal("explicit account", err)
	}
	items, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(items)
	if bytes.Contains(raw, []byte("secret")) {
		t.Fatal("metadata contains secret")
	}
	if _, err = s.Save(ctx, SaveRequest{Service: first.Service, Account: first.Account, Name: first.Name, ExpectedRevision: first.Revision, Token: "stale-write"}); err == nil {
		t.Fatal("stale revision accepted")
	}
	if err = s.Remove(ctx, RemoveRequest{Service: second.Service, Account: second.Account, ExpectedRevision: second.Revision}); err != nil {
		t.Fatal(err)
	}
	env, err = s.Environment(ctx, "run", bindings, false)
	if err != nil || env["GH_TOKEN"] != "first-secret" {
		t.Fatal("default not reassigned", err)
	}
}
