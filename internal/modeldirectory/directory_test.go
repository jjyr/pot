package modeldirectory

import (
	"context"
	"encoding/json"
	"github.com/jjyr/pot/internal/core"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func ptr[T any](v T) *T { return &v }
func fixture() map[string]provider {
	return map[string]provider{"proxy": {Name: "Proxy", NPM: "@ai-sdk/openai-compatible", API: ptr("https://fixture.invalid/v1"), Models: map[string]model{"claude-example": {Name: "Proxy chat", Limit: Limits{Context: 64000, Input: 50000, Output: 4000}, ToolCall: true, Modalities: modalities{Input: []string{"text", "image"}, Output: []string{"text"}}}, "embedding": {Kind: ptr("embedding")}}}, "anthropic": {Name: "Anthropic", NPM: "@ai-sdk/anthropic", Models: map[string]model{"claude-example": {Name: "Official", Limit: Limits{Context: 200000, Output: 32000}, ToolCall: true, Modalities: modalities{Input: []string{"text"}, Output: []string{"text"}}}}}}
}
func profile() core.Profile {
	return core.Profile{ID: "p", Revision: 1, Name: "P", Provider: "route", AuthRef: "provider:route", Model: "claude-example", ModelSettings: &core.ModelSettings{}, Params: core.Parameters{Agent: core.DefaultAgentSettings(), MaxOutputTokens: 4000}, ToolPolicy: core.ToolPolicy{SummaryReadOnly: true, Unattended: "guest_vm"}}
}
func TestOfflineDirectoryAndExactProviderDefaults(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "models"))
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Options()) < 8 || len(d.Defaults()) < 100 {
		t.Fatal("incomplete offline seed")
	}
	d.state = snapshot{Providers: fixture(), Logos: map[string]string{}}
	p := profile()
	automatic, err := d.Apply(p)
	if err != nil || automatic.Params.Agent.ContextWindow != 200000 || automatic.Params.MaxOutputTokens != 32000 {
		t.Fatal(automatic, err)
	}
	p.ModelSettings.CatalogProvider = ptr("proxy")
	resolved, err := d.Apply(p)
	if err != nil || resolved.Params.Agent.MaxInputTokens == nil || *resolved.Params.Agent.MaxInputTokens != 50000 {
		t.Fatal(resolved, err)
	}
	p.ModelSettings.ContextWindow = ptr(uint64(16000))
	p.ModelSettings.MaxOutputTokens = ptr(uint64(2000))
	resolved, err = d.Apply(p)
	if err != nil || resolved.Params.MaxOutputTokens != 2000 || resolved.Params.Agent.ContextWindow != 16000 {
		t.Fatal(resolved, err)
	}
	p.ModelSettings.MaxOutputTokens = ptr(uint64(5000))
	if _, err = d.Apply(p); err == nil {
		t.Fatal("accepted output beyond model maximum")
	}
	if p.Params.Agent.ContextWindow != 32768 {
		t.Fatal("mutated editable profile")
	}
	options := d.Options()
	for _, p := range options {
		for _, m := range p.Models {
			if m.ID == "embedding" {
				t.Fatal("offered specialized model")
			}
		}
	}
}
func TestRefreshAttemptDurableAndConditionalAtomic(t *testing.T) {
	root := t.TempDir()
	d, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api" {
			w.WriteHeader(404)
			return
		}
		if r.Header.Get("If-None-Match") == "seed" {
			w.WriteHeader(304)
			return
		}
		w.Header().Set("ETag", "seed")
		json.NewEncoder(w).Encode(fixture())
	}))
	defer server.Close()
	d.source = server.URL + "/api"
	d.logoBase = server.URL + "/logos/"
	d.client = server.Client()
	if err = d.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if d.Defaults()["claude-example"].Context != 200000 {
		t.Fatal("refresh not published")
	}
	d, err = Open(root)
	if err != nil {
		t.Fatal(err)
	}
	delay, err := d.delay(time.Now())
	if err != nil || delay < 23*time.Hour {
		t.Fatal(delay, err)
	}
	d.client = server.Client()
	d.source = server.URL + "/api"
	if err = d.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls < 2 {
		t.Fatal(calls)
	}
	d.source = server.URL + "/failure"
	if err = d.Refresh(context.Background()); err == nil {
		t.Fatal("failed refresh accepted")
	}
	if d.Defaults()["claude-example"].Context != 200000 {
		t.Fatal("failed refresh replaced catalog")
	}
	delay, _ = d.delay(time.Now())
	if delay < 23*time.Hour {
		t.Fatal("failed attempt not persisted")
	}
}
