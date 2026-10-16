// Package modeldirectory owns public model facts. Inference and editing read a
// local snapshot; only the independent lifecycle refresh contacts models.dev.
package modeldirectory

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/instance"
	"github.com/jjyr/pot/resources"
)

const maxBytes = 16 * 1024 * 1024

var Common = []string{"openai", "anthropic", "google", "deepseek", "openrouter", "opencode", "opencode-go", "groq"}

type Limits struct {
	Context uint64 `json:"context"`
	Input   uint64 `json:"input"`
	Output  uint64 `json:"output"`
}
type route struct {
	NPM *string `json:"npm"`
	API *string `json:"api"`
}
type modalities struct {
	Input  []string `json:"input"`
	Output []string `json:"output"`
}
type model struct {
	Name       string     `json:"name"`
	Limit      Limits     `json:"limit"`
	Modalities modalities `json:"modalities"`
	ToolCall   bool       `json:"tool_call"`
	Provider   route      `json:"provider"`
	Status     *string    `json:"status"`
	Kind       *string    `json:"type"`
}
type provider struct {
	Name   string           `json:"name"`
	NPM    string           `json:"npm"`
	API    *string          `json:"api"`
	Doc    *string          `json:"doc"`
	Models map[string]model `json:"models"`
}
type snapshot struct {
	Providers map[string]provider `json:"providers"`
	Logos     map[string]string   `json:"logos"`
	ETag      *string             `json:"etag"`
}
type Directory struct {
	mu               sync.RWMutex
	root             string
	state            snapshot
	client           *http.Client
	source, logoBase string
}
type ModelOption struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Endpoint string `json:"endpoint"`
	Protocol string `json:"protocol"`
	Context  uint64 `json:"context"`
	Input    uint64 `json:"input"`
	Output   uint64 `json:"output"`
	Image    bool   `json:"image"`
}
type ProviderOption struct {
	ID       string        `json:"id"`
	Name     string        `json:"name"`
	Endpoint string        `json:"endpoint"`
	Protocol string        `json:"protocol"`
	Doc      *string       `json:"doc"`
	Common   bool          `json:"common"`
	Logo     string        `json:"logo"`
	Models   []ModelOption `json:"models"`
}

func validID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-_.", c)) {
			return false
		}
	}
	return true
}
func validate(providers map[string]provider) error {
	if len(providers) == 0 || len(providers) > 2000 {
		return errors.New("invalid model directory provider count")
	}
	for id, p := range providers {
		if !validID(id) || p.Name == "" || len(p.Name) > 256 || len(p.Models) > 10000 {
			return errors.New("invalid provider metadata")
		}
		for id, m := range p.Models {
			if id == "" || len(id) > 256 || len(m.Name) > 512 || strings.ContainsFunc(id, unicode.IsControl) || m.Limit.Context > core.MaxSafeInteger || m.Limit.Input > core.MaxSafeInteger || m.Limit.Output > core.MaxSafeInteger {
				return errors.New("invalid model metadata")
			}
		}
	}
	return nil
}
func Open(root string) (*Directory, error) {
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	d := &Directory{root: root, client: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) > 3 || r.URL.Scheme != "https" || r.URL.Hostname() != "models.dev" {
			return errors.New("model metadata redirect rejected")
		}
		return nil
	}}, source: "https://models.dev/api.json?type=all", logoBase: "https://models.dev/logos/"}
	if err := json.Unmarshal(resources.ModelDirectory, &d.state); err != nil {
		return nil, err
	}
	if err := validate(d.state.Providers); err != nil {
		return nil, err
	}
	if body, err := os.ReadFile(filepath.Join(root, "catalog.json")); err == nil && len(body) <= maxBytes {
		var cached snapshot
		if json.Unmarshal(body, &cached) == nil && validate(cached.Providers) == nil {
			d.state = cached
		}
	}
	return d, nil
}
func endpoint(id string, p provider, m *model) (string, string, bool) {
	npm := p.NPM
	api := p.API
	if m != nil {
		if m.Provider.NPM != nil {
			npm = *m.Provider.NPM
		}
		if m.Provider.API != nil {
			api = m.Provider.API
		}
	}
	protocol := ""
	switch npm {
	case "@ai-sdk/openai-compatible", "@openrouter/ai-sdk-provider", "@ai-sdk/groq", "@ai-sdk/cerebras", "@ai-sdk/deepinfra":
		protocol = "openai_chat"
	case "@ai-sdk/openai":
		protocol = "openai_responses"
	case "@ai-sdk/anthropic":
		protocol = "anthropic_messages"
	case "@ai-sdk/google":
		protocol = "gemini"
	default:
		return "", "", false
	}
	if api == nil {
		value := map[string]string{"openai": "https://api.openai.com/v1", "anthropic": "https://api.anthropic.com/v1", "google": "https://generativelanguage.googleapis.com/v1beta", "groq": "https://api.groq.com/openai/v1", "cerebras": "https://api.cerebras.ai/v1", "deepinfra": "https://api.deepinfra.com/v1/openai"}[id]
		if value == "" {
			return "", "", false
		}
		api = &value
	}
	u, err := url.Parse(*api)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", "", false
	}
	base := strings.TrimRight(*api, "/")
	if u.Path == "" || u.Path == "/" {
		base += "/v1"
	}
	suffix := map[string]string{"openai_chat": "/chat/completions", "openai_responses": "/responses", "anthropic_messages": "/messages", "gemini": "/models/{model}:streamGenerateContent"}[protocol]
	if !strings.HasSuffix(base, suffix) {
		base += suffix
	}
	return base, protocol, true
}
func usable(m model) bool {
	return m.Kind == nil && (m.Status == nil || *m.Status != "deprecated") && m.ToolCall && slices.Contains(m.Modalities.Input, "text") && slices.Contains(m.Modalities.Output, "text")
}
func (d *Directory) Options() []ProviderOption {
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := []ProviderOption{}
	for id, p := range d.state.Providers {
		models := []ModelOption{}
		for mid, m := range p.Models {
			ep, protocol, ok := endpoint(id, p, &m)
			if !ok || !usable(m) {
				continue
			}
			models = append(models, ModelOption{mid, m.Name, ep, protocol, m.Limit.Context, m.Limit.Input, m.Limit.Output, slices.Contains(m.Modalities.Input, "image")})
		}
		if len(models) == 0 {
			continue
		}
		sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
		ep, protocol, ok := endpoint(id, p, nil)
		if !ok {
			ep, protocol = models[0].Endpoint, models[0].Protocol
		}
		logo := d.state.Logos[id]
		if !validLogo(logo) {
			logo = ""
		}
		out = append(out, ProviderOption{id, p.Name, ep, protocol, p.Doc, slices.Contains(Common, id), logo, models})
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := slices.Index(Common, out[i].ID), slices.Index(Common, out[j].ID)
		if a < 0 {
			a = len(Common)
		}
		if b < 0 {
			b = len(Common)
		}
		if a != b {
			return a < b
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}
func official(model string) []string {
	families := []struct{ prefixes, providers []string }{{[]string{"gpt-", "chatgpt-", "o1", "o3", "o4"}, []string{"openai"}}, {[]string{"claude-"}, []string{"anthropic"}}, {[]string{"gemini-", "gemma-"}, []string{"google"}}, {[]string{"deepseek-"}, []string{"deepseek"}}, {[]string{"qwen", "qwq-", "qvq-"}, []string{"alibaba", "alibaba-cn"}}, {[]string{"kimi-", "moonshot-"}, []string{"moonshotai", "moonshotai-cn"}}, {[]string{"glm-"}, []string{"zhipuai", "zai"}}, {[]string{"grok-"}, []string{"xai"}}, {[]string{"mistral-", "open-mistral-", "magistral-", "ministral-", "codestral-", "devstral-", "pixtral-", "voxtral-"}, []string{"mistral"}}, {[]string{"command-", "north-", "aya-", "tiny-aya-"}, []string{"cohere"}}, {[]string{"minimax-"}, []string{"minimax", "minimax-cn"}}, {[]string{"mimo-"}, []string{"xiaomi"}}, {[]string{"mercury-"}, []string{"inception"}}, {[]string{"sonar"}, []string{"perplexity"}}, {[]string{"morph-"}, []string{"morph"}}, {[]string{"step-", "stepaudio-"}, []string{"stepfun"}}, {[]string{"muse-"}, []string{"meta"}}}
	for _, f := range families {
		for _, p := range f.prefixes {
			if strings.HasPrefix(strings.ToLower(model), p) {
				return f.providers
			}
		}
	}
	return nil
}
func find(providers map[string]provider, catalog *string, id string) (model, bool) {
	if catalog != nil {
		m, ok := providers[*catalog].Models[id]
		return m, ok
	}
	for _, p := range official(id) {
		if m, ok := providers[p].Models[id]; ok {
			return m, true
		}
	}
	ids := []string{}
	for p := range providers {
		ids = append(ids, p)
	}
	sort.Strings(ids)
	for _, p := range ids {
		if m, ok := providers[p].Models[id]; ok {
			return m, true
		}
	}
	return model{}, false
}
func (d *Directory) Defaults() map[string]Limits {
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := map[string]Limits{}
	for _, p := range d.state.Providers {
		for id := range p.Models {
			m, _ := find(d.state.Providers, nil, id)
			out[id] = m.Limit
		}
	}
	return out
}
func (d *Directory) Apply(profile core.Profile) (core.Profile, error) {
	data, _ := json.Marshal(profile)
	var result core.Profile
	if err := json.Unmarshal(data, &result); err != nil {
		return result, err
	}
	settings := result.ModelSettings
	if settings == nil {
		return result, result.Validate()
	}
	d.mu.RLock()
	m, known := find(d.state.Providers, settings.CatalogProvider, result.Model)
	d.mu.RUnlock()
	limits := m.Limit
	window := limits.Context
	if window == 0 {
		window = 32768
	}
	if settings.ContextWindow != nil {
		window = *settings.ContextWindow
	}
	if window < 2 || window > core.MaxSafeInteger {
		return result, errors.New("context window must exceed one and use supported precision")
	}
	output := limits.Output
	if output == 0 {
		output = 8192
	}
	output = min(output, window-1)
	if settings.MaxOutputTokens != nil {
		output = *settings.MaxOutputTokens
	}
	if output == 0 || output >= window || limits.Output > 0 && output > limits.Output {
		return result, errors.New("output limit must be positive, below context and within model maximum")
	}
	result.Params.Agent.ContextWindow = window
	result.Params.Agent.ContextReserve = min(result.Params.Agent.ContextReserve, window/4)
	result.Params.MaxOutputTokens = output
	if limits.Input > 0 {
		n := limits.Input
		result.Params.Agent.MaxInputTokens = &n
	} else {
		result.Params.Agent.MaxInputTokens = nil
	}
	if known {
		catalog := result.Params.Agent.ModelCatalog
		if catalog == nil {
			catalog = &core.ModelCatalog{SchemaVersion: 1, Default: json.RawMessage(`{}`), Models: map[string]map[string]json.RawMessage{}}
		}
		modalities := []string{"text"}
		if slices.Contains(m.Modalities.Input, "image") {
			modalities = append(modalities, "image")
		}
		defaults := map[string]any{"input_modalities": modalities}
		var overrides any
		if err := json.Unmarshal(catalog.Default, &overrides); err != nil {
			return result, err
		}
		catalog.Default, _ = json.Marshal(core.MergeJSON(defaults, overrides))
		result.Params.Agent.ModelCatalog = catalog
	}
	return result, result.Validate()
}
func validLogo(value string) bool {
	v := strings.ToLower(value)
	if len(v) > 128*1024 || !strings.Contains(v, "<svg") || !strings.Contains(v, "</svg>") {
		return false
	}
	for _, bad := range []string{"<script", "<image", "<foreignobject", "<!entity", "<!doctype", "href", "url(", "onload", "onerror", "javascript:"} {
		if strings.Contains(v, bad) {
			return false
		}
	}
	return true
}
func (d *Directory) Run(ctx context.Context) {
	for {
		delay, err := d.delay(time.Now())
		if err != nil {
			delay = 24 * time.Hour
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		_ = d.Refresh(ctx)
	}
}
func (d *Directory) delay(now time.Time) (time.Duration, error) {
	var check struct {
		AttemptedAt int64 `json:"attempted_at"`
	}
	body, err := os.ReadFile(filepath.Join(d.root, "check.json"))
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if err = json.Unmarshal(body, &check); err != nil {
		return 0, err
	}
	due := time.UnixMilli(check.AttemptedAt).Add(24 * time.Hour).Sub(now)
	return max(0, due), nil
}
func (d *Directory) Refresh(ctx context.Context) error {
	if err := instance.WriteJSON(filepath.Join(d.root, "check.json"), map[string]int64{"attempted_at": time.Now().UnixMilli()}); err != nil {
		return err
	}
	d.mu.RLock()
	etag := d.state.ETag
	d.mu.RUnlock()
	request, err := http.NewRequestWithContext(ctx, "GET", d.source, nil)
	if err != nil {
		return err
	}
	if etag != nil {
		request.Header.Set("If-None-Match", *etag)
	}
	response, err := d.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode == 304 {
		return nil
	}
	if response.StatusCode != 200 {
		return errors.New("model directory upstream unavailable")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil || len(data) > maxBytes {
		return errors.New("model directory response exceeds limit")
	}
	var providers map[string]provider
	if err = json.Unmarshal(data, &providers); err != nil {
		return err
	}
	if err = validate(providers); err != nil {
		return err
	}
	d.mu.RLock()
	logos := map[string]string{}
	for id, logo := range d.state.Logos {
		logos[id] = logo
	}
	d.mu.RUnlock()
	jobs := make(chan string)
	var lock sync.Mutex
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for id := range jobs {
				req, err := http.NewRequestWithContext(ctx, "GET", d.logoBase+url.PathEscape(id)+".svg", nil)
				if err != nil {
					continue
				}
				resp, err := d.client.Do(req)
				if err != nil {
					continue
				}
				data, readErr := io.ReadAll(io.LimitReader(resp.Body, 128*1024+1))
				resp.Body.Close()
				if readErr == nil && resp.StatusCode == 200 && validLogo(string(data)) {
					lock.Lock()
					logos[id] = string(data)
					lock.Unlock()
				}
			}
		})
	}
	for _, id := range Common {
		if _, ok := providers[id]; ok {
			select {
			case jobs <- id:
			case <-ctx.Done():
				close(jobs)
				wg.Wait()
				return ctx.Err()
			}
		}
	}
	close(jobs)
	wg.Wait()
	next := snapshot{Providers: providers, Logos: logos}
	if value := response.Header.Get("ETag"); value != "" {
		next.ETag = &value
	}
	if err = instance.WriteJSON(filepath.Join(d.root, "catalog.json"), next); err != nil {
		return err
	}
	d.mu.Lock()
	d.state = next
	d.mu.Unlock()
	return nil
}
