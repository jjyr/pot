package control

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/assistant"
	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/execution"
	"github.com/jjyr/pot/pkg/storage"
)

func allowanceFixture(t *testing.T, handler http.HandlerFunc) (*Service, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	root := t.TempDir()
	pluginRoot := filepath.Join(root, "plugins")
	writeTaskFixture(t, filepath.Join(pluginRoot, "quota-fixture", "plugin.json"), `{"schema_version":2,"id":"quota-fixture","version":"1","contributes":{"model_providers":{"providers":[{"id":"gateway","name":"Fixture Gateway","protocol":"openai_chat","capabilities":{"catalog":"none","allowances":true}}],"adapter":{"runtime":"node","execution":"control","entrypoint":"provider.mjs","files":["provider.mjs"],"credential_env":"FIXTURE_KEY"}}}}`)
	writeTaskFixture(t, filepath.Join(pluginRoot, "quota-fixture", "provider.mjs"), `export async function read(input) { const response=await fetch(input.connection.endpoint,{headers:{Authorization:'Bearer '+process.env.FIXTURE_KEY}});if(response.status===403)return {error:{code:'permission'}};if(!response.ok)return {error:{code:'unavailable'}};return {allowances:await response.json()};}`)
	manager, err := execution.New("allowance-control", "boot", execution.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	keys, _ := secrets.NewMemoryKey(make([]byte, 32))
	s, err := Open(Options{Maintenance: func() bool { return true }, Root: root, RuntimeID: "allowance-control", BootID: "boot", PluginRoot: pluginRoot, Keys: keys, Manager: manager})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close(); manager.Stop() })
	// The fixture drives jobs explicitly, keeping automatic summaries and timing
	// outside the metadata/acceptance contracts under test.
	s.pluginMu.Lock()
	t.Cleanup(s.pluginMu.Unlock)
	return s, server
}
func fixtureQuota(w http.ResponseWriter, used float64) {
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	json.NewEncoder(w).Encode(core.AllowanceSnapshot{ReadAt: stamp, Accounts: []core.ProviderAllowance{{Name: "Anonymous plan", Plan: "Pro", ReadAt: stamp, AsOf: stamp, IdentityQuality: "ambiguous", Windows: []core.AllowanceWindow{{Name: "Five hours", Used: &used, IdentityQuality: "ambiguous"}}}}})
}
func saveAllowanceProfile(t *testing.T, s *Service, id, endpoint, key string) core.Profile {
	t.Helper()
	profile, err := s.Profiles.Save(context.Background(), SaveProfile{ProviderTypeID: "quota-fixture/gateway", RequestID: "save-" + id, ProfileID: id, Name: id, Model: "fixture", Endpoint: endpoint, Protocol: "openai_chat", APIKey: key, MaxOutputTokens: 100})
	if err != nil {
		t.Fatal(err)
	}
	return profile
}
func allowanceBindings(t *testing.T, s *Service) []assistant.BackgroundBinding {
	t.Helper()
	if err := s.ensureProviderAllowanceBindings(context.Background()); err != nil {
		t.Fatal(err)
	}
	bindings, err := s.Mailbox.Backgrounds(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	result := []assistant.BackgroundBinding{}
	for _, b := range bindings {
		if b.ConnectionID != "" {
			result = append(result, b)
		}
	}
	return result
}
func TestProviderAllowanceDefaultsCredentialsWaitingAndOffPersistence(t *testing.T) {
	ctx := context.Background()
	var mu sync.Mutex
	credentials := map[string]int{}
	s, server := allowanceFixture(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		credentials[r.Header.Get("Authorization")]++
		mu.Unlock()
		fixtureQuota(w, 95)
	})
	saveAllowanceProfile(t, s, "first", server.URL, "isolated-first-key")
	saveAllowanceProfile(t, s, "second", server.URL, "isolated-second-key")
	bindings := allowanceBindings(t, s)
	if len(bindings) != 2 {
		t.Fatal(bindings)
	}
	for _, b := range bindings {
		c, err := decodeAllowanceConfig(b.Config)
		if err != nil || !b.Enabled || c.Threshold != 90 || b.Assistant != "" {
			t.Fatal(b, c, err)
		}
	}
	s.runProviderAllowance(ctx, bindings[0], storage.Now())
	bindings = allowanceBindings(t, s)
	var waiting allowanceMonitorCheckpoint
	if err := json.Unmarshal(bindings[0].State, &waiting); err != nil || waiting.Policy.Active {
		t.Fatal(waiting, err)
	}
	owner, err := s.Registry.Create(ctx, "first-assistant", "First")
	if err != nil {
		t.Fatal(err)
	}
	bindings = allowanceBindings(t, s)
	for _, b := range bindings {
		if b.Assistant != owner.ID {
			t.Fatal(b)
		}
		s.runProviderAllowance(ctx, b, storage.Now())
	}
	events, err := s.Mailbox.Notifications(ctx, owner.ID)
	if err != nil || len(events) != 2 {
		t.Fatal(events, err)
	}
	for _, b := range allowanceBindings(t, s) {
		s.runProviderAllowance(ctx, b, storage.Now())
	}
	events, err = s.Mailbox.Notifications(ctx, owner.ID)
	if err != nil || len(events) != 2 {
		t.Fatal("duplicate warning", events, err)
	}
	mu.Lock()
	if credentials["Bearer isolated-first-key"] == 0 || credentials["Bearer isolated-second-key"] == 0 {
		t.Error(credentials)
	}
	mu.Unlock()
	view, err := s.ConnectionAllowances(ctx, "first")
	if err != nil || view.Snapshot == nil || view.Monitor.Threshold != 90 {
		t.Fatal(view, err)
	}
	if err = s.ConfigureConnectionAllowance(ctx, "first", ProviderAllowanceMonitorRequest{Enabled: false, Threshold: 75, Assistant: owner.ID, Revision: view.Monitor.Revision}); err != nil {
		t.Fatal(err)
	}
	if err = s.ensureProviderAllowanceBindings(ctx); err != nil {
		t.Fatal(err)
	}
	refreshed, err := s.RefreshConnectionAllowances(ctx, "first")
	if err != nil || refreshed.Monitor.Enabled || refreshed.Monitor.Threshold != 75 {
		t.Fatal(refreshed, err)
	}
	if err = s.ConfigureConnectionAllowance(ctx, "first", ProviderAllowanceMonitorRequest{Enabled: true, Threshold: 90, Assistant: owner.ID, Revision: view.Monitor.Revision}); err == nil {
		t.Fatal("stale owner revision accepted")
	}
	pluginView, err := s.PluginNotifications(ctx)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(pluginView)
	var projection struct {
		Items []struct {
			ConnectionID string `json:"connection_id"`
		}
	}
	if err = json.Unmarshal(raw, &projection); err != nil {
		t.Fatal(err)
	}
	for _, b := range projection.Items {
		if b.ConnectionID != "" {
			t.Fatal("connection monitor leaked into background extension settings", b)
		}
	}
}
func TestProviderAllowancePermissionDegradesMetadataOnly(t *testing.T) {
	ctx := context.Background()
	s, server := allowanceFixture(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) })
	profile := saveAllowanceProfile(t, s, "forbidden", server.URL, "permission-key")
	owner, err := s.Registry.Create(ctx, "assistant", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	bindings := allowanceBindings(t, s)
	s.runProviderAllowance(ctx, bindings[0], storage.Now())
	view, err := s.ConnectionAllowances(ctx, profile.ID)
	if err != nil || view.Status != "permission" || view.Monitor.Status != "permission" || view.Snapshot != nil {
		t.Fatal(view, err)
	}
	if _, err = s.Profiles.Resolve(profile.ID); err != nil {
		t.Fatal("metadata permission failure disabled inference", err)
	}
	events, err := s.Mailbox.Notifications(ctx, owner.ID)
	if err != nil || len(events) != 0 {
		t.Fatal(events, err)
	}
}
func TestProviderAllowanceRetargetDuringReadFencesCheckpoint(t *testing.T) {
	ctx := context.Background()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	s, server := allowanceFixture(t, func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(entered) })
		<-release
		fixtureQuota(w, 99)
	})
	saveAllowanceProfile(t, s, "retarget", server.URL, "retarget-key")
	first, err := s.Registry.Create(ctx, "first", "First")
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Registry.Create(ctx, "second", "Second")
	if err != nil {
		t.Fatal(err)
	}
	bindings := allowanceBindings(t, s)
	if bindings[0].Assistant != first.ID {
		t.Fatal(bindings)
	}
	done := make(chan struct{})
	go func() { defer close(done); s.runProviderAllowance(ctx, bindings[0], storage.Now()) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("adapter did not start")
	}
	if err = s.ConfigureConnectionAllowance(ctx, "retarget", ProviderAllowanceMonitorRequest{Enabled: true, Threshold: 90, Assistant: second.ID, Revision: bindings[0].Revision}); err != nil {
		close(release)
		t.Fatal(err)
	}
	close(release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("adapter did not finish")
	}
	current := allowanceBindings(t, s)[0]
	if string(current.State) != "null" {
		t.Fatal("stale response advanced checkpoint", string(current.State))
	}
	for _, id := range []string{first.ID, second.ID} {
		events, e := s.Mailbox.Notifications(ctx, id)
		if e != nil || len(events) != 0 {
			t.Fatal(events, e)
		}
	}
	s.runProviderAllowance(ctx, current, storage.Now())
	events, err := s.Mailbox.Notifications(ctx, second.ID)
	if err != nil || len(events) != 1 {
		t.Fatal(events, err)
	}
	if events[0].Plugin.ConnectionID != "retarget" || events[0].Plugin.Identity.ID != "quota-fixture" {
		t.Fatal(events)
	}
}

func TestProviderAllowanceRestartKeepsEpisodesAndOwnerSettings(t *testing.T) {
	ctx := context.Background()
	s, server := allowanceFixture(t, func(w http.ResponseWriter, r *http.Request) { fixtureQuota(w, 98) })
	saveAllowanceProfile(t, s, "kept", server.URL, "restart-key")
	owner, err := s.Registry.Create(ctx, "first", "First")
	if err != nil {
		t.Fatal(err)
	}
	bindings := allowanceBindings(t, s)
	s.runProviderAllowance(ctx, bindings[0], storage.Now())
	view, err := s.ConnectionAllowances(ctx, "kept")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ConfigureConnectionAllowance(ctx, "kept", ProviderAllowanceMonitorRequest{Enabled: false, Threshold: 80, Assistant: owner.ID, Revision: view.Monitor.Revision}); err != nil {
		t.Fatal(err)
	}
	options := s.options
	// Balance the fixture's cleanup lock while stopping every old service loop.
	s.pluginMu.Unlock()
	err = s.Close()
	s.pluginMu.Lock()
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(options)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	reopened.pluginMu.Lock()
	defer reopened.pluginMu.Unlock()
	after, err := reopened.ConnectionAllowances(ctx, "kept")
	if err != nil || after.Monitor.Enabled || after.Monitor.Threshold != 80 {
		t.Fatal(after, err)
	}
	// Re-enable with the previous threshold so the persisted episode, rather
	// than a deliberately changed policy, is what suppresses duplicate delivery.
	if err = reopened.ConfigureConnectionAllowance(ctx, "kept", ProviderAllowanceMonitorRequest{Enabled: true, Threshold: 90, Assistant: owner.ID, Revision: after.Monitor.Revision}); err != nil {
		t.Fatal(err)
	}
	current := allowanceBindings(t, reopened)[0]
	reopened.runProviderAllowance(ctx, current, storage.Now())
	events, err := reopened.Mailbox.Notifications(ctx, owner.ID)
	if err != nil || len(events) != 1 {
		t.Fatal("restart replayed warning", events, err)
	}
}

func TestProviderAllowanceReenableProjectsPendingUntilNextPoll(t *testing.T) {
	ctx := context.Background()
	s, server := allowanceFixture(t, func(w http.ResponseWriter, r *http.Request) { fixtureQuota(w, 50) })
	profile := saveAllowanceProfile(t, s, "toggle", server.URL, "toggle-key")
	owner, err := s.Registry.Create(ctx, "owner", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	config, _ := json.Marshal(allowanceMonitorConfig{Threshold: 90})
	// A persisted disabled instance can precede host default initialization.
	// Enabling changes owner intent without rewriting its producer checkpoint.
	if err = s.Mailbox.ConfigureBackground(ctx, assistant.BackgroundBinding{ID: allowanceInstanceID(profile.ID, "quota-fixture"), PluginID: "quota-fixture", ConnectionID: profile.ID, Enabled: false, Assistant: owner.ID, Config: config}); err != nil {
		t.Fatal(err)
	}
	before, err := s.ConnectionAllowances(ctx, profile.ID)
	if err != nil || before.Monitor.Enabled || before.Monitor.Status != "disabled" {
		t.Fatal(before, err)
	}
	if err = s.ConfigureConnectionAllowance(ctx, profile.ID, ProviderAllowanceMonitorRequest{Enabled: true, Threshold: 90, Assistant: owner.ID, Revision: before.Monitor.Revision}); err != nil {
		t.Fatal(err)
	}
	after, err := s.ConnectionAllowances(ctx, profile.ID)
	if err != nil || !after.Monitor.Enabled || after.Monitor.Status != "pending" || after.Monitor.Revision != before.Monitor.Revision+1 {
		t.Fatal(after, err)
	}
	if err = s.ConfigureConnectionAllowance(ctx, profile.ID, ProviderAllowanceMonitorRequest{Enabled: false, Threshold: 90, Assistant: owner.ID, Revision: after.Monitor.Revision}); err != nil {
		t.Fatal(err)
	}
	off, err := s.ConnectionAllowances(ctx, profile.ID)
	if err != nil || off.Monitor.Status != "disabled" {
		t.Fatal(off, err)
	}
}
