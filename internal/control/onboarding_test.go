package control

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/modelprotocol"
)

func TestBootstrapWaitsForProviderAndCreatesExactlyOnePot(t *testing.T) {
	s, profile := configurationService(t)
	ctx := context.Background()
	s.Profiles.mu.Lock()
	s.Profiles.state.DefaultProviderID = ""
	s.Profiles.mu.Unlock()
	result, err := s.BootstrapAssistant(ctx, "zh-CN")
	if err != nil || result["assistant"] != nil {
		t.Fatal(result, err)
	}
	s.Profiles.mu.Lock()
	s.Profiles.state.DefaultProviderID = profile.ID
	s.Profiles.mu.Unlock()
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Go(func() {
			if _, err := s.BootstrapAssistant(ctx, "zh-CN"); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	definitions, err := s.Registry.Definitions(ctx)
	if err != nil || len(definitions) != 1 || definitions[0].Name != "Pot" {
		t.Fatal(definitions, err)
	}
	a := definitions[0]
	c, _ := s.Conversation(ctx, a.ID)
	snapshot, _ := c.Snapshot(ctx)
	if len(snapshot.Messages) != 1 || !strings.HasPrefix(snapshot.Messages[0].Text, "你好，我是 Pot。") {
		t.Fatal(snapshot)
	}
	config, err := s.AssistantConfiguration(ctx, a.ID)
	if err != nil || config.Config.Model != profile.Model || config.Config.SourceProfileID != profile.ID {
		t.Fatal(config, err)
	}
	if _, err = s.Registry.Rename(ctx, a.ID, "小白", a.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = s.BootstrapAssistant(ctx, "en"); err != nil {
		t.Fatal(err)
	}
	snapshot, _ = c.Snapshot(ctx)
	if len(snapshot.Messages) != 1 || !strings.HasPrefix(snapshot.Messages[0].Text, "你好，我是 Pot。") {
		t.Fatal("locale switch changed history", snapshot)
	}
	other, err := s.CreateAssistant(ctx, CreateAssistant{RequestID: "second", Name: "Research", Locale: "en"})
	if err != nil {
		t.Fatal(err)
	}
	otherChat, _ := s.Conversation(ctx, other.ID)
	otherSnapshot, _ := otherChat.Snapshot(ctx)
	if len(otherSnapshot.Messages) != 1 || !strings.HasPrefix(otherSnapshot.Messages[0].Text, "Hi, I'm Research.") {
		t.Fatal(otherSnapshot)
	}
}

func TestChatRenameIsOwnerScopedAndRetrySafe(t *testing.T) {
	s, _ := configurationService(t)
	ctx := context.Background()
	a, err := s.CreateAssistant(ctx, CreateAssistant{RequestID: "create", Name: "Pot"})
	if err != nil {
		t.Fatal(err)
	}
	c, _ := s.Conversation(ctx, a.ID)
	thread, _ := c.Thread(ctx)
	call := modelprotocol.ToolCall{ID: "rename", Name: "rename_assistant", Arguments: json.RawMessage(`{"name":"小白"}`)}
	dispatch := s.productDispatch(a.ID, "turn", thread.Generation, false)[call.Name]
	if _, err = dispatch(ctx, core.ExecutionContext{Owner: a.ID}, call, nil); err != nil {
		t.Fatal(err)
	}
	definitions, _ := s.Registry.Definitions(ctx)
	renamed := definitions[0]
	if renamed.Name != "小白" || renamed.Revision != 2 {
		t.Fatal(renamed)
	}
	if _, err = s.Registry.Rename(ctx, a.ID, "Later", renamed.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = dispatch(ctx, core.ExecutionContext{Owner: a.ID}, call, nil); err != nil {
		t.Fatal(err)
	}
	definitions, _ = s.Registry.Definitions(ctx)
	if definitions[0].Name != "Later" || definitions[0].Revision != 3 {
		t.Fatal("retry overwrote later name", definitions)
	}
	peer := s.productDispatchBound(a.ID, "peer-chat", "turn", thread.Generation, false)[call.Name]
	if _, err = peer(ctx, core.ExecutionContext{Owner: "peer-chat"}, call, nil); err == nil {
		t.Fatal("peer renamed assistant")
	}
	stale := s.productDispatch(a.ID, "stale-turn", thread.Generation+1, false)[call.Name]
	if _, err = stale(ctx, core.ExecutionContext{Owner: a.ID}, call, nil); err == nil {
		t.Fatal("stale generation renamed assistant")
	}
	if len(s.productTools(true)) != 0 {
		t.Fatal("worker inherited rename tool")
	}
}
