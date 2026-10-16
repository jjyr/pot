package control

import (
	"context"
	"encoding/json"
	"github.com/jjyr/pot/internal/core"
	"github.com/jjyr/pot/internal/gateway"
	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/internal/usage"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func modelSession(t *testing.T, endpoint string) *ModelSession {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	key, _ := secrets.NewMemoryKey(make([]byte, 32))
	registry, err := gateway.Open(filepath.Join(root, "control.sqlite"), key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { registry.Close() })
	u, _ := url.Parse(endpoint)
	if err = registry.Save(ctx, gateway.Provider{ID: "test", Endpoint: endpoint, AllowedHosts: []string{u.Hostname()}, Protocol: "openai_chat", SecretRef: "provider/test"}, []byte("private-test")); err != nil {
		t.Fatal(err)
	}
	ledger, err := usage.Open(filepath.Join(root, "usage.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ledger.Close() })
	settings := core.DefaultAgentSettings()
	settings.Plugins = map[string]core.PluginConfig{}
	p := core.Profile{ID: "test", Revision: 1, Name: "test", Provider: "test", Model: "test", AuthRef: "provider:test", Params: core.Parameters{MaxOutputTokens: 100, Agent: settings}, ToolPolicy: core.ToolPolicy{SummaryReadOnly: true, Unattended: "guest_vm"}}
	spec, err := p.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	return &ModelSession{Registry: registry, Usage: ledger, Spec: spec, Scope: "main", Turn: "turn", Generation: 1, Role: "main", Claim: func(context.Context, string) error { return nil }, Clock: func() time.Time { return time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC) }}
}
func request(id string) core.ModelRequest {
	return core.ModelRequest{ID: id, Protocol: "openai_chat", Provider: "test", Model: "test", Body: json.RawMessage(`{"model":"test","stream":true,"messages":[{"role":"user","content":"hello"}],"max_tokens":100}`)}
}
func TestModelReservationSettlementAndDuplicateFence(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"include_usage":true`) {
			t.Error("missing usage request")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":5}}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	s := modelSession(t, server.URL)
	ctx := context.Background()
	if err := s.Usage.SetPolicy(ctx, usage.Policy{Period: "day", Limit: 10000, Timezone: "UTC", AutoResume: true}); err != nil {
		t.Fatal(err)
	}
	stream, err := s.Model(ctx, request("request-1"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.ReadAll(stream); err != nil {
		t.Fatal(err)
	}
	stream.Close()
	q, err := s.Usage.Quota(ctx, s.Clock().UnixMilli())
	if err != nil || q.Used != 17 || q.Reserved != 0 {
		t.Fatal(q, err)
	}
	if _, err = s.Model(ctx, request("request-1")); err == nil {
		t.Fatal("resent duplicate model request")
	}
	if calls.Load() != 1 {
		t.Fatal(calls.Load())
	}
}
func TestModelPartialUsageAndAllowanceBeforeNetwork(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		io.WriteString(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":12}}\n\n")
	}))
	defer server.Close()
	s := modelSession(t, server.URL)
	ctx := context.Background()
	s.Usage.SetPolicy(ctx, usage.Policy{Period: "day", Limit: 10000, Timezone: "UTC", AutoResume: true})
	stream, err := s.Model(ctx, request("partial"))
	if err != nil {
		t.Fatal(err)
	}
	io.ReadAll(stream)
	stream.Close()
	q, err := s.Usage.Quota(ctx, s.Clock().UnixMilli())
	if err != nil || q.Reserved == 0 {
		t.Fatal(q, err)
	}
	s.Usage.SetPolicy(ctx, usage.Policy{Period: "day", Limit: 10, Timezone: "UTC", AutoResume: true})
	if _, err = s.Model(ctx, request("paused")); err == nil {
		t.Fatal("ignored token allowance")
	}
	if calls.Load() != 1 {
		t.Fatal(calls.Load())
	}
	wrong := request("wrong")
	wrong.Model = "other"
	if _, err = s.Model(ctx, wrong); err == nil {
		t.Fatal("accepted changed model")
	}
}

func TestDeepSeekCompatibilityUsesPinnedServiceMetadataInsteadOfRouteID(t *testing.T) {
	var thinking atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		json.NewDecoder(r.Body).Decode(&body)
		thinking.Store(string(body["thinking"]) == `{"type":"disabled"}`)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":5}}\n\n")
	}))
	defer server.Close()
	service := taskResourceService(t)
	settings := core.DefaultAgentSettings()
	settings.Plugins = map[string]core.PluginConfig{}
	brand := "deepseek"
	output := uint64(128)
	profile, err := service.Profiles.Save(context.Background(), SaveProfile{RequestID: "brand", ProfileID: "deepseek-profile", Name: "DeepSeek", Model: "deepseek-v4-flash", Endpoint: server.URL, Protocol: "openai_chat", APIKey: "fixture", Agent: &settings, ModelSettings: &core.ModelSettings{CatalogProvider: &brand, MaxOutputTokens: &output}})
	if err != nil {
		t.Fatal(err)
	}
	spec, err := service.Profiles.Resolve(profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := service.Providers.Get(context.Background(), profile.Provider)
	if err != nil || provider.ID == "deepseek" || provider.APIProvider != "deepseek" {
		t.Fatal(provider, err)
	}
	session := ModelSession{Registry: service.Providers, Usage: service.Usage, Spec: spec, Scope: "main", Turn: "turn", Generation: 1, Role: "main", Claim: func(context.Context, string) error { return nil }}
	body, _ := json.Marshal(map[string]any{"model": spec.Model, "stream": true, "messages": []any{map[string]string{"role": "user", "content": "hello"}}, "max_tokens": 128})
	stream, err := session.Model(context.Background(), core.ModelRequest{ID: "brand-request", Provider: spec.Provider, Model: spec.Model, Protocol: spec.GatewayProtocol, Body: body})
	if err != nil {
		t.Fatal(err)
	}
	io.ReadAll(stream)
	stream.Close()
	if !thinking.Load() {
		t.Fatal("immutable generated route dropped DeepSeek wire policy")
	}
}
