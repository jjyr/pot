package control

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/assistant"
	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/pkg/agent"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/execution"
	"github.com/jjyr/pot/pkg/modelprotocol"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

// Explicit opt-in only. Read the selected machine's existing provider through a
// read-only SQLite connection; credentials remain in memory on that machine.
// All messages, checkpoints, tool calls and accounting belong to a private root.
func TestConfiguredModelMessageReferencesLive(t *testing.T) {
	if os.Getenv("POT_LIVE") != "1" || os.Getenv("POT_REFERENCE_LIVE_ROOT") == "" {
		t.Skip("requires POT_LIVE=1 and explicit POT_REFERENCE_LIVE_ROOT on the configured machine")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	profile, provider, credential := configuredLiveModel(t, ctx, os.Getenv("POT_REFERENCE_LIVE_ROOT"))
	t.Logf("configured model=%s protocol=%s", profile.Model, provider.Protocol)
	manager, err := execution.New("reference-live", "boot", execution.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	keys, _ := secrets.NewMemoryKey(make([]byte, 32))
	root := t.TempDir()
	service, err := Open(Options{Context: ctx, Root: root, RuntimeID: "reference-live", BootID: "boot", Workspace: filepath.Join(root, "workspace"), AgentHome: filepath.Join(root, "agent"), Manager: manager, Keys: keys})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		service.Close()
		manager.Stop()
		drain, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		if err := manager.Drain(drain); err != nil {
			t.Error(err)
		}
	}()
	if _, err = service.Registry.Create(ctx, "create", "Reference qualification"); err != nil {
		t.Fatal(err)
	}
	low := "low"
	saved, err := service.Profiles.Save(ctx, SaveProfile{RequestID: "profile", ProfileID: "reference-live", Name: "Reference qualification", Model: profile.Model, Endpoint: provider.Endpoint, Protocol: provider.Protocol, APIKey: string(credential), MaxOutputTokens: 512, ReasoningEffort: &low})
	if err != nil {
		t.Fatal(err)
	}
	spec, err := service.Profiles.Resolve(saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	allowed := []string{}
	spec.Params.Agent.BuiltinTools = []string{}
	spec.Params.Agent.AllowedTools = &allowed
	spec.Params.Agent.Plugins = map[string]core.PluginConfig{}
	spec.Params.Agent.AutoCompact = false
	spec.Params.Agent.RequestTimeoutMS = 60000
	if err = spec.Rehash(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "qualification.sqlite")
	var conversation *assistant.Conversation
	options := assistant.ConversationOptions{Context: ctx, Owner: "main", RuntimeID: "reference-live", BootID: "boot", Manager: manager, InitialSpec: &spec, InitialWelcome: "方案 A：预算 73，地点杭州，代号 JADE。方案 B：预算 29，地点苏州，代号 PEARL。", MaxRequests: 6, MaxTurnRequests: 3, Prepare: func(ctx context.Context, thread core.Thread, request core.TurnRequest) (assistant.RunOptions, error) {
		return service.prepare(ctx, conversation, thread, request)
	}}
	conversation, err = assistant.OpenConversation(path, options)
	if err != nil {
		t.Fatal(err)
	}
	service.conversations["main"] = conversation
	snapshot, _ := conversation.Snapshot(ctx)
	source := snapshot.Messages[0].ID
	send := func(id, text string, refs []core.MessageReference) protocol.Snapshot {
		t.Helper()
		if _, err := conversation.Send(ctx, protocol.SendMessage{RequestID: id, ConversationID: "main", Text: text, References: refs}); err != nil {
			t.Fatal(err)
		}
		updates, remove, err := conversation.Subscribe(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer remove()
		for {
			select {
			case <-ctx.Done():
				t.Fatal("live reference test timed out")
			case snap := <-updates:
				if snap.Turn == nil {
					if snap.Agent.Detail != nil {
						t.Fatal("live model failed", *snap.Agent.Detail)
					}
					return snap
				}
			}
		}
	}
	snapshot = send("selected", "我选中的是哪个方案？只回答方案、预算、地点和代号。", []core.MessageReference{{MessageID: source, Quote: "方案 A：预算 73"}})
	response := snapshot.Messages[len(snapshot.Messages)-1].Text
	if !strings.Contains(response, "73") || !strings.Contains(response, "杭州") || !strings.Contains(response, "JADE") || strings.Contains(response, "PEARL") {
		t.Fatal("model did not identify selected source", response)
	}
	t.Log("selected excerpt understood:", response)
	generated := snapshot.Messages[len(snapshot.Messages)-1].ID
	snapshot = send("replay", "我选中的这条回复写了哪个代号？只回复代号。", []core.MessageReference{{MessageID: generated, Quote: "代号"}})
	response = snapshot.Messages[len(snapshot.Messages)-1].Text
	if !strings.Contains(response, "JADE") || strings.Contains(response, "PEARL") {
		t.Fatal("native replay lost the selected assistant content", response)
	}
	t.Log("selected assistant content understood with native replay:", response)

	if err = conversation.Close(); err != nil {
		t.Fatal(err)
	}
	// Install the same minimal retained-history shape as a completed compaction,
	// with no original message text. The transcript remains the source of truth.
	store, err := storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := agent.Checkpoint{Type: "context", Version: 2, Messages: []modelprotocol.Message{{Kind: modelprotocol.MessageKind{Type: "summary"}, Role: "user", Text: "The conversation discussed two plans. Original details have been compacted away."}}, Pending: []modelprotocol.ToolCall{}, Receipts: []agent.ToolReceipt{}, SteeringIDs: []string{}}
	compacted, _ := json.Marshal(checkpoint)
	if err = store.SaveContext(ctx, compacted); err != nil {
		t.Fatal(err)
	}
	store.Close()
	options.InitialWelcome = ""
	conversation, err = assistant.OpenConversation(path, options)
	if err != nil {
		t.Fatal(err)
	}
	service.conversations["main"] = conversation
	snapshot = send("compacted", "只回复所引用原消息中方案 B 的地点和代号。", []core.MessageReference{{MessageID: source}})
	response = snapshot.Messages[len(snapshot.Messages)-1].Text
	for _, message := range snapshot.Messages {
		if message.Tool != nil && message.Tool.Name == "read_chat_messages" {
			t.Fatal("references must not require a model-side message lookup")
		}
	}
	if !strings.Contains(response, "苏州") || !strings.Contains(response, "PEARL") {
		t.Fatal("model lost supplied reference content after compaction", response)
	}
	t.Log("whole-message reference supplied directly after compaction:", response)
}
