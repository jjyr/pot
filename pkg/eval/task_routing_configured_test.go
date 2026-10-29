package eval

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/gateway"
	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/pkg/agent"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/modelprotocol"
	"github.com/jjyr/pot/pkg/storage"
)

const configuredRoutingOutputCap uint64 = 4096

// Existing configuration is read only. Only an explicitly selected profile is
// loaded; its credential stays in this process and is never included in reports.
func loadConfiguredRoutingProfile(ctx context.Context, root, profileID string) (core.Profile, gateway.Provider, []byte, error) {
	fail := func(err error) (core.Profile, gateway.Provider, []byte, error) {
		return core.Profile{}, gateway.Provider{}, nil, err
	}
	if root == "" || !filepath.IsAbs(root) || profileID == "" {
		return fail(errors.New("absolute configured root and exact profile ID are required"))
	}
	raw, err := os.ReadFile(filepath.Join(root, "profiles.json"))
	if err != nil {
		return fail(err)
	}
	defer clear(raw)
	var config struct {
		Snapshot struct {
			Profiles []core.Profile `json:"profiles"`
		} `json:"snapshot"`
	}
	if err = json.Unmarshal(raw, &config); err != nil {
		return fail(errors.New("invalid configured profile snapshot"))
	}
	var profile core.Profile
	matches := 0
	for _, candidate := range config.Snapshot.Profiles {
		if candidate.ID == profileID {
			profile = candidate
			matches++
		}
	}
	if matches != 1 || profile.Provider == "" || profile.Model == "" {
		return fail(errors.New("exactly one matching configured profile is required"))
	}
	path := filepath.Join(root, "control.sqlite")
	db, err := storage.OpenDatabaseReadOnly(path, storage.ControlDatabase)
	if err != nil {
		return fail(err)
	}
	defer db.Close()
	var metadata string
	if err = db.QueryRowContext(ctx, "SELECT metadata FROM providers WHERE id=?", profile.Provider).Scan(&metadata); err != nil {
		return fail(errors.New("selected provider metadata is unavailable"))
	}
	var provider gateway.Provider
	if err = json.Unmarshal([]byte(metadata), &provider); err != nil {
		return fail(errors.New("invalid selected provider metadata"))
	}
	if provider.ID != profile.Provider || provider.Protocol != "openai_chat" && provider.Protocol != "openai_responses" {
		return fail(errors.New("selected provider identity or protocol is unsupported"))
	}
	if err = provider.Validate(); err != nil {
		return fail(err)
	}
	store, err := secrets.OpenReadOnly(path, secrets.SystemKey{Account: "task-routing-live"})
	if err != nil {
		return fail(err)
	}
	defer store.Close()
	credential, err := store.Get(ctx, provider.SecretRef)
	if err != nil {
		return fail(errors.New("selected provider credential unavailable"))
	}
	if len(credential) == 0 {
		clear(credential)
		return fail(errors.New("selected provider credential is empty"))
	}
	return profile, provider, credential, nil
}

func routingConfiguredModel(t *testing.T) routingModel {
	t.Helper()
	if os.Getenv("POT_LIVE") != "1" {
		t.Skip("requires POT_LIVE=1")
	}
	root, selected := os.Getenv("POT_ROUTING_CONFIGURED_ROOT"), os.Getenv("POT_ROUTING_PROFILE")
	if root == "" || selected == "" {
		t.Fatal("configured routing requires POT_ROUTING_CONFIGURED_ROOT and POT_ROUTING_PROFILE")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	profile, provider, credential, err := loadConfiguredRoutingProfile(ctx, root, selected)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { clear(credential) })
	return routingModel{Name: profile.ID, Profile: profile, Protocol: provider.Protocol, OutputCap: configuredRoutingOutputCap,
		NewServices: func(_ string) (agent.Services, func(), error) {
			key := append([]byte(nil), credential...)
			return &configuredRoutingServices{profile: profile, provider: provider, credential: key}, func() { clear(key) }, nil
		},
	}
}

type configuredRoutingServices struct {
	profile    core.Profile
	provider   gateway.Provider
	credential []byte
}

func (s *configuredRoutingServices) Model(ctx context.Context, request core.ModelRequest) (io.ReadCloser, error) {
	unsent := func(err error) (io.ReadCloser, error) {
		return nil, &core.ModelRequestError{Cause: err, Disposition: "unsent"}
	}
	if request.ID == "" || request.Provider != s.profile.Provider || request.Protocol != s.provider.Protocol || request.Model != s.profile.Model || len(request.Body) > 96*1024*1024 {
		return unsent(errors.New("routing request differs from selected configuration"))
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(request.Body, &fields); err != nil {
		return unsent(errors.New("invalid model request JSON"))
	}
	var model string
	var stream bool
	if json.Unmarshal(fields["model"], &model) != nil || json.Unmarshal(fields["stream"], &stream) != nil || model != s.profile.Model || !stream {
		return unsent(errors.New("invalid model or streaming mode"))
	}
	body, _, err := modelprotocol.Protocol(s.provider.Protocol).Clamp(request.Body, configuredRoutingOutputCap)
	if err != nil {
		return unsent(err)
	}
	// Match Control's pinned provider adaptation, not a separate eval wire format.
	if s.provider.Protocol == "openai_chat" && s.provider.APIProvider == "deepseek" {
		if err = json.Unmarshal(body, &fields); err != nil {
			return unsent(err)
		}
		fields["thinking"] = json.RawMessage(`{"type":"disabled"}`)
		body, err = json.Marshal(fields)
		if err != nil {
			return unsent(err)
		}
	}
	response, err := gateway.Forward(ctx, s.provider, s.credential, body, "", 60*time.Second)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		response.Body.Close()
		return nil, &core.ModelRequestError{Cause: gateway.StatusError(response.StatusCode), Disposition: "rejected"}
	}
	return response.Body, nil
}
func (*configuredRoutingServices) Compaction(context.Context, json.RawMessage) error {
	return errors.New("routing evaluation does not permit compaction")
}

func TestConfiguredRoutingSelectionIsExactAndReadOnly(t *testing.T) {
	root := t.TempDir()
	keyPath := filepath.Join(root, "key")
	t.Setenv("POT_SECRET_KEY_FILE", keyPath)
	if err := os.WriteFile(keyPath, bytes.Repeat([]byte{17}, 32), 0600); err != nil {
		t.Fatal(err)
	}
	profile := core.Profile{ID: "selected", Provider: "configured-provider", Model: "configured-model", Params: core.Parameters{Agent: core.DefaultAgentSettings()}}
	other := profile
	other.ID = "first"
	other.Provider = "unrelated-provider"
	snapshot := func(profiles ...core.Profile) {
		raw, _ := json.Marshal(map[string]any{"snapshot": map[string]any{"profiles": profiles}})
		if err := os.WriteFile(filepath.Join(root, "profiles.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	snapshot(other, profile)
	path := filepath.Join(root, "control.sqlite")
	if _, _, _, err := loadConfiguredRoutingProfile(context.Background(), root, "missing"); err == nil {
		t.Fatal("missing profile selected a fallback")
	}
	if _, _, _, err := loadConfiguredRoutingProfile(context.Background(), root, profile.ID); err == nil {
		t.Fatal("missing database was accepted")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("read-only load created database")
	}
	registry, err := gateway.Open(path, secrets.FileKey{Path: keyPath})
	if err != nil {
		t.Fatal(err)
	}
	provider := gateway.Provider{ID: profile.Provider, Endpoint: "https://example.invalid/responses", AllowedHosts: []string{"example.invalid"}, Protocol: "openai_responses", SecretRef: "selected-secret"}
	if err = registry.Save(context.Background(), provider, []byte("fixture-credential")); err != nil {
		registry.Close()
		t.Fatal(err)
	}
	registry.Close()
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got, route, credential, err := loadConfiguredRoutingProfile(context.Background(), root, profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != profile.ID || route.ID != provider.ID || string(credential) != "fixture-credential" {
		clear(credential)
		t.Fatal("selected profile/credential mismatch")
	}
	clear(credential)
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("configured reader changed database")
	}
	snapshot(profile, profile)
	if _, _, _, err = loadConfiguredRoutingProfile(context.Background(), root, profile.ID); err == nil {
		t.Fatal("ambiguous profile accepted")
	}
	snapshot(profile)
	if err = os.Remove(keyPath); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = loadConfiguredRoutingProfile(context.Background(), root, profile.ID); err == nil {
		t.Fatal("missing key accepted")
	}
	if _, err = os.Stat(keyPath); !os.IsNotExist(err) {
		t.Fatal("configured reader recreated master key")
	}
}

func TestConfiguredRoutingGatewayUsesNativeProtocols(t *testing.T) {
	for _, protocol := range []string{"openai_chat", "openai_responses"} {
		t.Run(protocol, func(t *testing.T) {
			var calls atomic.Int32
			captured := make(chan map[string]any, 2)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Header.Get("Authorization") != "Bearer fixture-credential" {
					t.Error("gateway did not bind credential")
				}
				var body map[string]any
				if json.NewDecoder(r.Body).Decode(&body) != nil {
					t.Error("invalid wire body")
				}
				captured <- body
				if calls.Load() == 2 {
					w.WriteHeader(403)
					io.WriteString(w, "private-provider-response")
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, "data: fixture\n\n")
			}))
			defer server.Close()
			profile := core.Profile{Provider: "selected-provider", Model: "selected-model"}
			provider := gateway.Provider{ID: profile.Provider, Endpoint: server.URL, AllowedHosts: []string{"127.0.0.1"}, Protocol: protocol, SecretRef: "fixture-secret"}
			field := "max_output_tokens"
			if protocol == "openai_chat" {
				provider.APIProvider = "deepseek"
				field = "max_tokens"
			}
			service := &configuredRoutingServices{profile: profile, provider: provider, credential: []byte("fixture-credential")}
			body, _ := json.Marshal(map[string]any{"model": profile.Model, "stream": true, field: 8192, "reasoning_effort": "high"})
			request := core.ModelRequest{ID: "request", Protocol: protocol, Provider: profile.Provider, Model: profile.Model, Body: body}
			wrong := request
			wrong.Model = "other"
			_, err := service.Model(context.Background(), wrong)
			var outcome *core.ModelRequestError
			if !errors.As(err, &outcome) || outcome.Disposition != "unsent" || calls.Load() != 0 {
				t.Fatal("wrong identity reached gateway")
			}
			stream, err := service.Model(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := io.ReadAll(stream)
			stream.Close()
			if err != nil || string(raw) != "data: fixture\n\n" {
				t.Fatal("native stream changed")
			}
			sent := <-captured
			if sent[field] != float64(configuredRoutingOutputCap) || sent["reasoning_effort"] != "high" {
				t.Fatal("output cap or reasoning fields changed")
			}
			if protocol == "openai_chat" {
				thinking, _ := sent["thinking"].(map[string]any)
				if thinking["type"] != "disabled" {
					t.Fatal("DeepSeek differs from production adaptation")
				}
			} else if _, exists := sent["thinking"]; exists {
				t.Fatal("Responses acquired a chat-only field")
			}
			_, err = service.Model(context.Background(), request)
			if !errors.As(err, &outcome) || outcome.Disposition != "rejected" || strings.Contains(err.Error(), "private-provider-response") || strings.Contains(err.Error(), "fixture-credential") {
				t.Fatal("unsafe provider error classification")
			}
			if calls.Load() != 2 {
				t.Fatal("gateway retried request")
			}
		})
	}
}
