package pluginhost

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/plugin"
)

func modelReaderFixture(t *testing.T, source string) (plugin.Package, core.ModelProviderRequest, []string) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "reader.mjs"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	p := plugin.Package{Root: root, Manifest: core.PluginManifest{SchemaVersion: 2, ID: "provider-reader", Version: "1", Contributes: core.PluginContributions{ModelProviders: &core.PluginModelProviders{Providers: []core.ModelProviderDefinition{{ID: "test", Name: "Test", Protocol: "openai_responses", Capabilities: core.ModelProviderCapabilities{Catalog: "plugin", Allowances: true}}}, Adapter: &core.ModelProviderAdapter{Runtime: "node", Execution: "control", Entrypoint: "reader.mjs", Files: []string{"reader.mjs"}, CredentialEnv: "API_KEY"}}}}}
	r := core.ModelProviderRequest{Version: 1, Method: "list_models", Connection: core.ModelProviderConnection{ID: "one", Type: "test", Endpoint: "http://localhost:3425", Protocol: "openai_responses"}}
	worker, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return p, r, []string{worker}
}
func TestModelProviderRPCRejectsExtraFieldsWrongMethodAndCredentialLeaks(t *testing.T) {
	for _, scenario := range []struct {
		name, source string
		valid        bool
	}{
		{"valid", `if (input.version!==1 || input.connection.id!=='one' || input.connection.api_key || process.env.UNRELATED_SECRET) throw Error('scope');return {catalog:{models:[{id:'test-model',max_input:128,input_modalities:['text']}],read_at:'2026-01-01T00:00:00Z'}}`, true},
		{"credential", `return {catalog:{models:[{id:process.env.API_KEY}],read_at:'2026-01-01T00:00:00Z'}}`, false},
		{"extra_field", `return {catalog:{models:[],read_at:'2026-01-01T00:00:00Z'},sender:'owner'}`, false},
		{"wrong_method", `return {allowances:{accounts:[],read_at:'2026-01-01T00:00:00Z'}}`, false},
		{"exception", `throw Error(process.env.API_KEY)`, false},
		{"excess_output", `process.stdout.write('x'.repeat(4*1024*1024));return {}`, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			t.Setenv("UNRELATED_SECRET", "must-not-inherit")
			p, r, worker := modelReaderFixture(t, "export async function read(input){"+scenario.source+"}")
			result, err := RunModelProvider(context.Background(), "", p, r, map[string]string{"API_KEY": "fixture-private-token"}, worker)
			if (err == nil) != scenario.valid {
				t.Fatal("unexpected protocol result", err)
			}
			if err != nil && strings.Contains(err.Error(), "fixture-private-token") {
				t.Fatal("secret in diagnostic")
			}
			if scenario.valid && result.Catalog.Models[0].MaxInput != 128 {
				t.Fatal("model limit lost")
			}
		})
	}
}
func TestModelProviderRPCScopesCredentialAndRejectsSecretInput(t *testing.T) {
	p, r, worker := modelReaderFixture(t, `export async function read(){throw Error('must not run')}`)
	for _, credentials := range []map[string]string{{"OTHER": "secret"}, {"API_KEY": ""}, {"API_KEY": "secret", "OTHER": "secret"}} {
		if _, err := RunModelProvider(context.Background(), "", p, r, credentials, worker); err == nil || err.Error() != "model provider credential binding mismatch" {
			t.Fatal("wrong credential scope", err)
		}
	}
	r.Connection.Endpoint = "http://localhost:3425/fixture-private-token"
	if _, err := RunModelProvider(context.Background(), "", p, r, map[string]string{"API_KEY": "fixture-private-token"}, worker); err == nil || err.Error() != "credential in model provider request" {
		t.Fatal("credential in stdin", err)
	}
}
func TestModelProviderRPCDrainsDetachedDescendantsOnDeadline(t *testing.T) {
	p, r, worker := modelReaderFixture(t, `import {spawn} from 'node:child_process';import {writeFileSync} from 'node:fs';export async function read(){const child=spawn(process.execPath,['-e','setInterval(()=>{},1000)'],{detached:true,stdio:'ignore'});writeFileSync('child.pid',String(child.pid));await new Promise(()=>{});}`)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if _, err := RunModelProvider(ctx, "", p, r, nil, worker); err == nil || err.Error() != "model provider exchange timed out" {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(p.Root, "child.pid"))
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	if err = testProcessSignal(pid); !errors.Is(err, syscall.ESRCH) {
		t.Fatal("detached child survived", pid, err)
	}
}

func TestModelProviderCredentialRejectionIncludesEscapedStrings(t *testing.T) {
	for _, raw := range []string{`{"catalog":{"models":[{"id":"\u0066ixture-private-token"}]}}`, `{"fixtur\u0065-private-token":"value"}`} {
		if !containsPluginCredential([]byte(raw), "fixture-private-token") {
			t.Fatal("JSON escaping hid credential", raw)
		}
	}
	if containsPluginCredential([]byte(`{"catalog":{"models":[{"id":"public-model"}]}}`), "fixture-private-token") {
		t.Fatal("public metadata rejected")
	}
}

func TestModelProviderRPCPreservesOnlyTypedSafeFailure(t *testing.T) {
	p, r, worker := modelReaderFixture(t, `export async function read(){return {error:{code:'permission'}}}`)
	_, err := RunModelProvider(context.Background(), "", p, r, nil, worker)
	var failure *core.ModelProviderError
	if !errors.As(err, &failure) || failure.Code != "permission" {
		t.Fatal("safe failure lost", err)
	}
	for _, result := range []string{`{error:{code:'permission',message:'private detail'}}`, `{error:{code:'private detail'}}`} {
		if err := os.WriteFile(filepath.Join(p.Root, "reader.mjs"), []byte("export async function read(){return "+result+"}"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := RunModelProvider(context.Background(), "", p, r, nil, worker); err == nil || strings.Contains(err.Error(), "private detail") {
			t.Fatal("unsafe failure accepted", err)
		}
	}
}
