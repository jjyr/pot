package runtime_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/jjyr/pot/pkg/runtime"
)

type unusedKeys struct{ t *testing.T }

func (k unusedKeys) Key(context.Context, bool) ([]byte, error) {
	k.t.Error("opening an unconfigured host must not access credential keys")
	return nil, errors.New("unexpected credential access")
}

func hostOptions(t *testing.T) runtime.Options {
	t.Helper()
	return runtime.Options{
		Root: filepath.Join(t.TempDir(), "host"), ResourceDirectory: t.TempDir(),
		DisableMetadataRefresh: true,
		Keys:                   unusedKeys{t}, Embedded: true, Name: "Embedded fixture",
	}
}

func TestLifecycleRetainsIdentityAndReleasesRoot(t *testing.T) {
	options := hostOptions(t)
	host, err := runtime.Open(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = host.Close() })
	info := host.Info()
	if info.RuntimeID == "" || info.BootID == "" || !filepath.IsAbs(info.Root) || info.AssetDirectory != options.ResourceDirectory {
		t.Fatalf("unexpected host identity: %+v", info)
	}
	if host.Executions() == nil {
		t.Fatal("complete host must expose its reusable managers")
	}

	if duplicate, err := runtime.Open(context.Background(), options); err == nil {
		_ = duplicate.Close()
		t.Fatal("second host acquired an owned root")
	}
	if err := host.Close(); err != nil {
		t.Fatal(err)
	}
	if err := host.Close(); err != nil {
		t.Fatalf("repeated close: %v", err)
	}
	reopened, err := runtime.Open(context.Background(), options)
	if err != nil {
		t.Fatalf("root lock was not released: %v", err)
	}
	defer reopened.Close()
	if got := reopened.Info(); got.RuntimeID != info.RuntimeID || got.BootID == info.BootID {
		t.Fatalf("reopen must retain runtime identity and change boot: before=%+v after=%+v", info, got)
	}
	if mode, err := os.Stat(info.Root); err != nil || mode.Mode().Perm() != 0700 {
		t.Fatalf("host root is not private: info=%v err=%v", mode, err)
	}
}

func TestAuthenticatedHostAPI(t *testing.T) {
	host, err := runtime.Open(context.Background(), hostOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	handler := host.Handler("host-token")
	for _, authorization := range []string{"", "Bearer wrong-token"} {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/runtime", nil)
		request.Header.Set("Authorization", authorization)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("invalid credentials returned %d", recorder.Code)
		}
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/control", nil)
	request.Header.Set("Authorization", "Bearer host-token")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	var control struct{ ID, Name, Role string }
	if recorder.Code != http.StatusOK || json.Unmarshal(recorder.Body.Bytes(), &control) != nil {
		t.Fatalf("control API: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if control.ID != host.Info().RuntimeID || control.Name != "Embedded fixture" || control.Role != "embedded" {
		t.Fatalf("operator API lost embedding options: %+v", control)
	}
	request.Header.Set("X-Pot-Control-ID", "another-runtime")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("identity mismatch returned %d", recorder.Code)
	}
	request.Header.Del("X-Pot-Control-ID")
	request.Header.Set("Authorization", "Bearer ")
	recorder = httptest.NewRecorder()
	host.Handler("").ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("empty configured token returned %d", recorder.Code)
	}
	snapshot, err := host.ResourceSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var resources struct {
		RuntimeID string `json:"runtime_id"`
	}
	if err := json.Unmarshal(snapshot, &resources); err != nil {
		t.Fatal(err)
	}
	if resources.RuntimeID != host.Info().RuntimeID {
		t.Fatalf("unexpected resource snapshot: %s", snapshot)
	}
}

func TestExplicitPathsRequired(t *testing.T) {
	for _, options := range []runtime.Options{{}, {Root: filepath.Join(t.TempDir(), "host")}, {ResourceDirectory: t.TempDir()}} {
		if host, err := runtime.Open(context.Background(), options); err == nil {
			_ = host.Close()
			t.Fatal("host opened with implicit paths")
		}
	}
}
