package consumer_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/jjyr/pot/pkg/runtime"
	"github.com/jjyr/pot/pkg/sandbox"
)

type noCredentials struct{ t *testing.T }

func (k noCredentials) Key(context.Context, bool) ([]byte, error) {
	k.t.Error("unconfigured embedded host accessed credential keys")
	return nil, errors.New("credentials not available in external fixture")
}

func TestPublicRuntimeFromExternalModule(t *testing.T) {
	options := runtime.Options{
		Root: filepath.Join(t.TempDir(), "host"), ResourceDirectory: t.TempDir(),
		SandboxProviders: []sandbox.Provider{}, DisableMetadataRefresh: true,
		Keys: noCredentials{t}, Embedded: true, Name: "External fixture",
	}
	host, err := runtime.Open(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := host.Close(); err != nil {
			t.Error(err)
		}
	})
	info := host.Info()
	if info.RuntimeID == "" || info.BootID == "" || info.AssetDirectory != options.ResourceDirectory {
		t.Fatalf("host info: %+v", info)
	}
	if host.Executions() == nil || host.Resources() == nil || host.Provisioning() == nil {
		t.Fatal("host managers are inaccessible")
	}
	if len(host.Sandboxes().Descriptors()) != 0 {
		t.Fatal("empty providers unexpectedly selected a platform provider")
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/control", nil)
	recorder := httptest.NewRecorder()
	host.Handler("external-token").ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized API: %d", recorder.Code)
	}
	request.Header.Set("Authorization", "Bearer external-token")
	recorder = httptest.NewRecorder()
	host.Handler("external-token").ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("authenticated API: %d %s", recorder.Code, recorder.Body.String())
	}
	if err := host.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := runtime.Open(context.Background(), options)
	if err != nil {
		t.Fatalf("root lock was not released: %v", err)
	}
	defer reopened.Close()
	if next := reopened.Info(); next.RuntimeID != info.RuntimeID || next.BootID == info.BootID {
		t.Fatalf("reopen identity: before=%+v after=%+v", info, next)
	}
}
