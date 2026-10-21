package control

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/assistant"
	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/execution"
	"github.com/jjyr/pot/pkg/integrations"
	"github.com/jjyr/pot/pkg/modelprotocol"
	"github.com/jjyr/pot/pkg/protocol"
)

// The fake model exercises discovery and the production model -> dispatcher ->
// user-turn authorization -> shared configuration path, without paid requests.
func TestGitHubFiltersNativeToolToSharedStore(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var calls atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		n := calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		if n == 1 {
			if !strings.Contains(string(raw), "github_get_notification_filters") || !strings.Contains(string(raw), "github_update_notification_filters") {
				t.Error("tools not discovered")
			}
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"get\",\"type\":\"function\",\"function\":{\"name\":\"github_get_notification_filters\",\"arguments\":\"{}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
			return
		}
		if n == 2 {
			if !strings.Contains(string(raw), "mention") {
				t.Error("default missing from tool result")
			}
			args := `{"request_id":"native-save","revision":1,"enabled":true,"config":{"account_id":"fixture-account","repositories":["nervosnetwork/fiber"],"reasons":["mention"],"subject_types":["PullRequest"],"threads":null}}`
			encoded, _ := json.Marshal(args)
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"update\",\"type\":\"function\",\"function\":{\"name\":\"github_update_notification_filters\",\"arguments\":%s}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n", encoded)
			return
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Saved Pot notification filters.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer model.Close()
	manager, _ := execution.New("local", "boot", execution.Limits{MaxActive: 1, MaxQueued: 8})
	keys, _ := secrets.NewMemoryKey(make([]byte, 32))
	root, _ := filepath.Abs("../../plugins")
	s, err := Open(Options{Root: t.TempDir(), RuntimeID: "local", BootID: "boot", Manager: manager, Keys: keys, PluginRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// No background API calls: the fake account deliberately has no usable secret.
	s.pluginMu.Lock()
	defer s.pluginMu.Unlock()
	s.Registry.Create(ctx, "create", "Pot")
	if _, err = s.integrationAccounts.Save(ctx, integrations.Account{Service: "github", Identity: integrations.Identity{ID: "fixture-account", Username: "fixture"}, SecretRef: "missing"}, 0); err != nil {
		t.Fatal(err)
	}
	profile, err := s.Profiles.Save(ctx, SaveProfile{RequestID: "profile", ProfileID: "fixture", Name: "Fixture", Model: "fixture", Endpoint: model.URL, Protocol: "openai_chat", APIKey: "fixture", MaxOutputTokens: 256})
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := s.Profiles.Resolve(profile.ID)
	c, _ := s.Conversation(ctx, "main")
	if err = c.Configure(ctx, spec, 0); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Send(ctx, protocol.SendMessage{RequestID: "user-filter-request", ConversationID: "main", Text: "Only forward GitHub mentions on fiber PRs."}); err != nil {
		t.Fatal(err)
	}
	for {
		snapshot, _ := c.Snapshot(ctx)
		if snapshot.Turn == nil && calls.Load() >= 3 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("native tool timeout", snapshot)
		case <-time.After(time.Millisecond * 10):
		}
	}
	projection, err := s.PluginNotifications(ctx)
	if err != nil {
		t.Fatal(err)
	}
	b := projection.(map[string]any)["items"].([]assistant.BackgroundBinding)[0]
	if b.Revision != 2 || !strings.Contains(string(b.Config), "nervosnetwork/fiber") {
		t.Fatal(b)
	}
	var f githubFilters
	json.Unmarshal(b.Config, &f)
	f.Reasons = []string{"review_requested"}
	b.Config, _ = json.Marshal(f)
	if err = s.ConfigurePluginNotifications(ctx, "ui-save", b); err != nil {
		t.Fatal(err)
	}
	current, err := s.githubFilterCall(ctx, "main", modelprotocol.ToolCall{Name: "github_get_notification_filters", Arguments: json.RawMessage(`{}`)})
	if err != nil || !strings.Contains(string(current), "review_requested") {
		t.Fatal(string(current), err)
	}
	// UI retries use the same receipt; new requests with stale revisions conflict.
	if err = s.ConfigurePluginNotifications(ctx, "ui-save", b); err != nil {
		t.Fatal(err)
	}
	if err = s.ConfigurePluginNotifications(ctx, "new-stale", b); err == nil {
		t.Fatal("stale write accepted")
	}
	b.Enabled = false
	if err = s.ConfigurePluginNotifications(ctx, "ui-save", b); err == nil {
		t.Fatal("conflicting receipt accepted")
	}
	if _, err = s.githubFilterCall(ctx, "foreign", modelprotocol.ToolCall{Name: "github_get_notification_filters", Arguments: json.RawMessage(`{}`)}); err == nil {
		t.Fatal("cross assistant read")
	}
	thread, _ := c.Thread(ctx)
	call := modelprotocol.ToolCall{Name: "github_update_notification_filters", Arguments: json.RawMessage(`{}`)}
	for _, binding := range []struct {
		owner, execution, turn string
		generation             uint64
	}{{"main", "main", "expired", thread.Generation}, {"main", "main", "pot-result-event", thread.Generation}, {"main", "peer-session", "peer", thread.Generation}, {"main", "main", "expired", thread.Generation + 1}} {
		dispatch := s.productDispatchBound(binding.owner, binding.execution, binding.turn, binding.generation, false)[call.Name]
		if _, err = dispatch(ctx, core.ExecutionContext{Owner: binding.execution}, call, nil); err == nil {
			t.Fatal("untrusted turn configured filters", binding)
		}
	}
	if _, ok := s.productDispatch("main", "worker", 1, true)[call.Name]; ok {
		t.Fatal("worker inherited management")
	}
	accounts, _ := s.integrationAccounts.List(ctx)
	if _, err = s.integrationAccounts.Save(ctx, integrations.Account{Service: "github", Identity: integrations.Identity{ID: "different-account", Username: "other"}, SecretRef: "missing"}, accounts[0].Revision); err != nil {
		t.Fatal(err)
	}
	latest, _ := s.githubBinding(ctx)
	if err = s.ConfigurePluginNotifications(ctx, "cross-account", latest); err == nil || !strings.Contains(err.Error(), "github_account_changed") {
		t.Fatal("old account filters accepted", err)
	}
}

func TestGitHubFiltersRejectUnsupportedGranularity(t *testing.T) {
	valid := defaultGitHubFilters("123")
	for _, test := range []struct {
		field string
		value any
	}{{"reasons", []string{"direct_reply"}}, {"reasons", []string{"participating"}}, {"subject_types", []string{"Unknown"}}, {"repositories", []string{"not-a-repo"}}, {"threads", []string{"owner/repo#1701"}}, {"author", "me"}, {"control_id", "other"}} {
		var input map[string]any
		json.Unmarshal(valid, &input)
		input[test.field] = test.value
		raw, _ := json.Marshal(input)
		if err := validateGitHubNotificationConfig(raw); err == nil {
			t.Fatal("unsupported filter accepted", string(raw))
		}
	}
}
