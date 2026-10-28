package modeldirectory

import (
	"context"
	"encoding/json"
	"github.com/jjyr/pot/pkg/core"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jjyr/pot/resources"
)

func TestBundledProviderArtworkOverridesCachedLogos(t *testing.T) {
	root := t.TempDir()
	providers := fixture()
	providers["deepseek"] = providers["anthropic"]
	fallback := `<svg xmlns="http://www.w3.org/2000/svg"><path d="M0 0h24v24H0z"/></svg>`
	cached, err := json.Marshal(snapshot{Providers: providers, Logos: map[string]string{"deepseek": fallback, "proxy": fallback}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "catalog.json"), cached, 0600); err != nil {
		t.Fatal(err)
	}
	d, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	want, err := resources.ProviderLogos.ReadFile("provider-logos/deepseek.svg")
	if err != nil {
		t.Fatal(err)
	}
	for _, option := range d.Options() {
		switch option.ID {
		case "deepseek":
			if option.Logo != string(want) {
				t.Fatal("cached monochrome mark replaced bundled color artwork")
			}
		case "proxy":
			if option.Logo != fallback {
				t.Fatal("provider without bundled artwork lost its directory mark")
			}
		}
	}
	entries, err := resources.ProviderLogos.ReadDir("provider-logos")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		body, err := resources.ProviderLogos.ReadFile("provider-logos/" + entry.Name())
		if err != nil || !validLogo(string(body)) {
			t.Errorf("invalid bundled provider artwork %s: %v", entry.Name(), err)
		}
	}
}

func ptr[T any](v T) *T { return &v }
func fixture() map[string]provider {
	return map[string]provider{"proxy": {Name: "Proxy", NPM: "@ai-sdk/openai-compatible", API: ptr("https://fixture.invalid/v1"), Models: map[string]model{"claude-example": {Name: "Proxy chat", Limit: Limits{Context: 64000, Input: 50000, Output: 4000}, ToolCall: true, Modalities: modalities{Input: []string{"text", "image"}, Output: []string{"text"}}}, "embedding": {Kind: ptr("embedding")}}}, "anthropic": {Name: "Anthropic", NPM: "@ai-sdk/anthropic", Models: map[string]model{"claude-example": {Name: "Official", Limit: Limits{Context: 200000, Output: 32000}, ToolCall: true, Modalities: modalities{Input: []string{"text"}, Output: []string{"text"}}}}}}
}
func profile() core.Profile {
	return core.Profile{ID: "p", Revision: 1, Name: "P", Provider: "route", AuthRef: "provider:route", Model: "claude-example", ModelSettings: &core.ModelSettings{}, Params: core.Parameters{Agent: core.DefaultAgentSettings(), MaxOutputTokens: 4000}, ToolPolicy: core.ToolPolicy{SummaryReadOnly: true, Unattended: "runtime"}}
}
func TestOfflineDirectoryAndExactProviderDefaults(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "models"))
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Options()) < 8 || len(d.Defaults()) < 100 {
		t.Fatal("incomplete offline seed")
	}
	wantXAI, err := resources.ProviderLogos.ReadFile("provider-logos/xai.svg")
	if err != nil {
		t.Fatal(err)
	}
	foundGrok := false
	for _, option := range d.Options() {
		if option.ID == "xai" {
			foundGrok = option.Common && option.Name == "Grok" && option.Logo == string(wantXAI)
		}
	}
	if !foundGrok {
		t.Fatal("expected Grok (xai) as a common provider with bundled logo")
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

func TestProxyModelSuffixUsesExactFactsWithoutChangingRoute(t *testing.T) {
	d, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	providers := fixture()
	providers["openai"] = provider{Name: "OpenAI", Models: map[string]model{
		"gpt-example": {Name: "Official", Limit: Limits{Context: 200000, Input: 160000, Output: 32000}, Modalities: modalities{Input: []string{"text", "image", "pdf"}}},
	}}
	providers["proxy"].Models["gpt-example"] = model{Name: "Proxy", Limit: Limits{Context: 64000, Output: 4000}, Modalities: modalities{Input: []string{"text"}}}
	providers["proxy"].Models["exact/gpt-example"] = model{Name: "Full ID", Limit: Limits{Context: 80000, Output: 8000}, Modalities: modalities{Input: []string{"text"}}}
	d.state = snapshot{Providers: providers}
	for _, test := range []struct {
		name, model string
		catalog     *string
		window      uint64
		image, pdf  bool
	}{
		{"official suffix", "codex/gpt-example", nil, 200000, true, true},
		{"nested route", "gateway/codex/gpt-example", nil, 200000, true, true},
		{"full ID wins globally", "exact/gpt-example", nil, 80000, false, false},
		{"selected provider suffix", "codex/gpt-example", ptr("proxy"), 64000, false, false},
		{"selected provider is retained", "codex/gpt-example", ptr("anthropic"), 32768, false, false},
		{"unknown variant", "codex/gpt-example-new", nil, 32768, false, false},
		{"empty suffix", "codex/", nil, 32768, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := profile()
			p.Model = test.model
			p.ModelSettings.CatalogProvider = test.catalog
			resolved, err := d.Apply(p)
			if err != nil {
				t.Fatal(err)
			}
			if resolved.Model != p.Model || resolved.Provider != p.Provider || resolved.AuthRef != p.AuthRef || resolved.Params.Agent.ContextWindow != test.window {
				t.Fatal("route or inherited limits changed unexpectedly", resolved)
			}
			descriptor := core.DefaultModelDescriptor()
			if catalog := resolved.Params.Agent.ModelCatalog; catalog != nil {
				descriptor, err = catalog.Resolve(resolved.Provider, resolved.Model)
			}
			if err != nil || descriptor.Vision() != test.image || descriptor.PDF() != test.pdf {
				t.Fatal(descriptor, err)
			}
		})
	}
	// Explicit capability overrides remain attached to the actual wire model ID.
	p := profile()
	p.Model = "codex/gpt-example"
	p.Params.Agent.ModelCatalog = &core.ModelCatalog{SchemaVersion: 1, Default: json.RawMessage(`{}`), Models: map[string]map[string]json.RawMessage{
		p.Provider: {p.Model: json.RawMessage(`{"input_modalities":["text"]}`)},
	}}
	resolved, err := d.Apply(p)
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := resolved.Params.Agent.ModelCatalog.Resolve(p.Provider, p.Model)
	if err != nil || descriptor.Vision() || descriptor.PDF() {
		t.Fatal("suffix lookup replaced an explicit capability override", descriptor, err)
	}
}

func TestConnectionModalitiesOverrideDirectoryWithoutChangingIntent(t *testing.T) {
	d := &Directory{state: snapshot{Providers: fixture()}}
	p := profile()
	p.ModelSettings.CatalogProvider = ptr("proxy")
	p.ModelSettings.InputModalities = map[string][]string{p.Model: {"text", "pdf"}}
	resolved, err := d.Apply(p)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := resolved.Resolve()
	if err != nil || spec.AttachmentCapabilities() != (core.AttachmentCapabilities{PDF: true}) {
		t.Fatal(spec.AttachmentCapabilities(), err)
	}
	if p.Params.Agent.ModelCatalog != nil {
		t.Fatal("discovery mutated saved intent")
	}
	p.Params.Agent.ModelCatalog = &core.ModelCatalog{SchemaVersion: 1, Default: json.RawMessage(`{"input_modalities":["text","image"]}`), Models: map[string]map[string]json.RawMessage{}}
	resolved, err = d.Apply(p)
	if err != nil {
		t.Fatal(err)
	}
	spec, err = resolved.Resolve()
	if err != nil || spec.AttachmentCapabilities() != (core.AttachmentCapabilities{Image: true}) {
		t.Fatal("explicit model catalog override lost", err)
	}
	p.ModelSettings.InputModalities[p.Model] = []string{"image"}
	if _, err = d.Apply(p); err == nil {
		t.Fatal("invalid input facts accepted")
	}
}

func TestCachedNamespacedInputCapabilities(t *testing.T) {
	root := t.TempDir()
	providers := fixture()
	providers["openai"] = provider{Name: "OpenAI", Models: map[string]model{
		"gpt-example": {Name: "Example", Limit: Limits{Context: 500000, Output: 128000}, Modalities: modalities{Input: []string{"text", "image", "pdf"}}},
	}}
	data, err := json.Marshal(snapshot{Providers: providers})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "catalog.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	d, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		id      string
		catalog *string
		want    core.AttachmentCapabilities
	}{
		{"codex/gpt-example", nil, core.AttachmentCapabilities{Image: true, PDF: true}},
		{"openai/gpt-example", nil, core.AttachmentCapabilities{Image: true, PDF: true}},
		{"other/gpt-example", nil, core.AttachmentCapabilities{Image: true, PDF: true}},
		{"codex/openai/gpt-example", nil, core.AttachmentCapabilities{Image: true, PDF: true}},
		{"codex/gpt-example-pro", nil, core.AttachmentCapabilities{}},
		{"codex/claude-example", nil, core.AttachmentCapabilities{}},
		{"codex/gpt-example", ptr("proxy"), core.AttachmentCapabilities{}},
	} {
		t.Run(tt.id, func(t *testing.T) {
			p := profile()
			p.Model = tt.id
			p.ModelSettings.CatalogProvider = tt.catalog
			resolved, err := d.Apply(p)
			if err != nil {
				t.Fatal(err)
			}
			spec, err := resolved.Resolve()
			if err != nil || spec.AttachmentCapabilities() != tt.want {
				t.Fatal(spec.AttachmentCapabilities(), err)
			}
			window, output := uint64(32768), uint64(8192)
			if tt.want.Image {
				window, output = 500000, 128000
			} else if tt.id == "codex/claude-example" {
				window, output = 200000, 32000
			}
			if spec.Model != tt.id || spec.ContextWindow != window || spec.Params.MaxOutputTokens != output {
				t.Fatal("suffix facts changed request identity or inherited unexpected limits", spec.Model, spec.ContextWindow, spec.Params.MaxOutputTokens)
			}
		})
	}
	// An endpoint's exact declaration can limit an alias to image input only.
	p := profile()
	p.Model = "codex/gpt-example"
	p.ModelSettings.InputModalities = map[string][]string{p.Model: {"text", "image"}}
	resolved, err := d.Apply(p)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := resolved.Resolve()
	if err != nil || spec.AttachmentCapabilities() != (core.AttachmentCapabilities{Image: true}) {
		t.Fatal("cached PDF capability replaced connection facts", spec.AttachmentCapabilities(), err)
	}
	// Full identities from the directory win over normalized name fallback.
	p.ModelSettings.InputModalities = nil
	d.state.Providers["proxy"].Models[p.Model] = model{Name: "Exact", Modalities: modalities{Input: []string{"text"}}}
	resolved, err = d.Apply(p)
	if err != nil {
		t.Fatal(err)
	}
	spec, err = resolved.Resolve()
	if err != nil || spec.AttachmentCapabilities() != (core.AttachmentCapabilities{}) {
		t.Fatal("alias overrode exact model ID", spec.AttachmentCapabilities(), err)
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
