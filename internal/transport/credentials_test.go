package transport

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jjyr/pot/internal/control"
	product "github.com/jjyr/pot/internal/runtime"
	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/pkg/core"
)

func TestServiceAccountsWithoutAssistantAndRestart(t *testing.T) {
	ctx := context.Background()
	keys, _ := secrets.NewMemoryKey(make([]byte, 32))
	options := product.Options{Root: filepath.Join(t.TempDir(), "runtime"), DisableMetadataRefresh: true, Control: control.Options{Keys: keys}}
	runtime, err := product.Open(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	app := &Application{Control: runtime.Control}
	result, err := app.Call(ctx, Request{Method: "POST", Path: "/api/v1/credentials", Body: json.RawMessage(`{"service":"github","account":"personal","name":"Personal","token":"owner-fixture-token","expected_revision":0}`)})
	if err != nil {
		runtime.Close()
		t.Fatal(err)
	}
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), "owner-fixture-token") {
		t.Fatal("save returned secret")
	}
	if err = runtime.Close(); err != nil {
		t.Fatal(err)
	}
	runtime, err = product.Open(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	app.Control = runtime.Control
	result, err = app.Call(ctx, Request{Method: "GET", Path: "/api/v1/credentials"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(result)
	if !strings.Contains(string(raw), "personal") || strings.Contains(string(raw), "owner-fixture-token") {
		t.Fatal(string(raw))
	}
	values, err := runtime.Control.Credentials.Environment(ctx, "fixture-run", core.CredentialBindings{"GH_TOKEN": {Service: "github"}}, false)
	if err != nil || values["GH_TOKEN"] != "owner-fixture-token" {
		t.Fatal("credential not usable after restart", err)
	}
	_, err = app.Call(ctx, Request{Method: "POST", Path: "/api/v1/credentials/remove", Body: json.RawMessage(`{"service":"github","account":"personal","expected_revision":1}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = runtime.Control.Credentials.Environment(ctx, "fixture-run", core.CredentialBindings{"GH_TOKEN": {Service: "github"}}, false); err == nil {
		t.Fatal("deleted credential remained cached")
	}
}
