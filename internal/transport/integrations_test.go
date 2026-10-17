package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/jjyr/pot/pkg/storage"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/control"
	product "github.com/jjyr/pot/internal/runtime"
	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/pkg/integrations"
)

type loginAdapter struct{ done chan struct{} }

func (a loginAdapter) Descriptor() integrations.Descriptor {
	return integrations.Descriptor{ID: "github", Name: "GitHub", LoginAvailable: true}
}
func (a loginAdapter) Begin(context.Context) (integrations.Challenge, error) {
	return integrations.Challenge{URL: "https://example.test/authorize", UserCode: "TEST-CODE", ExpiresAt: time.Now().Add(time.Minute)}, nil
}
func (a loginAdapter) Authorize(ctx context.Context, _ integrations.Challenge) (integrations.Grant, error) {
	select {
	case <-ctx.Done():
		return integrations.Grant{}, ctx.Err()
	case <-a.done:
	}
	return integrations.Grant{Identity: integrations.Identity{ID: "123", Username: "fixture-user"}, Credential: integrations.Credential{AccessToken: "owner-fixture-token"}}, nil
}
func TestIntegrationLoginWithoutAssistantRestartAndLogout(t *testing.T) {
	ctx := context.Background()
	keys, _ := secrets.NewMemoryKey(make([]byte, 32))
	adapter := loginAdapter{make(chan struct{})}
	options := product.Options{Root: filepath.Join(t.TempDir(), "runtime"), DisableMetadataRefresh: true, Control: control.Options{Keys: keys, IntegrationAdapters: []integrations.Adapter{adapter}}}
	runtime, err := product.Open(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { runtime.Close() }()
	app := &Application{Control: runtime.Control}
	result, err := app.Call(ctx, Request{Method: "GET", Path: "/api/v1/integrations"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(result)
	if !strings.Contains(string(raw), "disconnected") {
		t.Fatal(string(raw))
	}
	result, err = app.Call(ctx, Request{Method: "POST", Path: "/api/v1/integrations/github/login", Body: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	login := result.(integrations.Login)
	close(adapter.done)
	for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(time.Millisecond) {
		value, err := app.Call(ctx, Request{Method: "GET", Path: "/api/v1/integrations/github/login/" + login.ID})
		if err != nil {
			t.Fatal(err)
		}
		if value.(integrations.Login).Status == "succeeded" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(value)
		}
	}
	if _, err = app.Call(ctx, Request{Method: "POST", Path: "/api/v1/integrations/github/login", Body: json.RawMessage(`{}`)}); err == nil {
		t.Fatal("second GitHub account accepted")
	}
	if err = runtime.Close(); err != nil {
		t.Fatal(err)
	}
	runtime, err = product.Open(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	app.Control = runtime.Control
	result, err = app.Call(ctx, Request{Method: "GET", Path: "/api/v1/integrations"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(result)
	if !strings.Contains(string(raw), "fixture-user") || strings.Contains(string(raw), "owner-fixture-token") {
		t.Fatal(string(raw))
	}
	token, err := runtime.Control.Integrations.Credential(ctx, "run", "github", false)
	if err != nil || token != "owner-fixture-token" {
		t.Fatal("credential after restart", err)
	}
	_, err = app.Call(ctx, Request{Method: "DELETE", Path: "/api/v1/integrations/github/connection", Body: json.RawMessage(`{"expected_revision":1}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = runtime.Control.Integrations.Credential(ctx, "run", "github", false); err == nil {
		t.Fatal("logout left cached token")
	}

	for _, suffix := range []string{"", "-wal", "-shm"} {
		raw, _ := os.ReadFile(filepath.Join(options.Root, "control.sqlite") + suffix)
		if bytes.Contains(raw, []byte("owner-fixture-token")) {
			t.Fatal("plaintext integration credential persisted")
		}
	}
	db, err := storage.OpenSQLite(filepath.Join(options.Root, "control.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err = db.QueryRow(`SELECT COUNT(*) FROM secrets`).Scan(&count); err != nil || count != 0 {
		t.Fatal("logout retained secret", count, err)
	}
	if _, err = app.Call(ctx, Request{Method: "POST", Path: "/api/v1/credentials", Body: json.RawMessage(`{}`)}); err == nil {
		t.Fatal("removed manual credential API accepted")
	}
}
