package control

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/jjyr/pot/pkg/execution"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

func TestRestartCompletionPublishesReceiverBeforeQueuedPreparation(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"next completed\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2}}\n\ndata: [DONE]\n\n"))
	}))
	defer server.Close()
	s := taskResourceService(t)
	ctx := context.Background()
	if _, err := legacyMainAssistant(ctx, s, "create", "Pot"); err != nil {
		t.Fatal(err)
	}
	profile, err := s.Profiles.Save(ctx, SaveProfile{RequestID: "profile", ProfileID: "test", Name: "Test", Model: "model", Endpoint: server.URL, Protocol: "openai_chat", APIKey: "fixture", MaxOutputTokens: 100})
	if err != nil {
		t.Fatal(err)
	}
	spec, err := s.Profiles.Resolve(profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.Conversation(ctx, "main")
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Configure(ctx, spec, 0); err != nil {
		t.Fatal(err)
	}
	options := s.options
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(options.Root, "assistants", "main", "conversation.sqlite")
	store, err := storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	first, next := "first", "next"
	for _, message := range []protocol.Message{
		{ID: "original-user", ConversationID: "main", Role: protocol.User, RequestID: &first, Text: "hello", Status: protocol.Complete},
		{ID: "original-reply", ConversationID: "main", Role: protocol.Assistant, ReplyTo: "original-user", Text: "already completed", Status: protocol.Complete},
		{ID: "next-user", ConversationID: "main", Role: protocol.User, RequestID: &next, Text: "next", Status: protocol.Accepted, Queued: true},
	} {
		if _, err = store.Save(ctx, message); err != nil {
			t.Fatal(err)
		}
	}
	request := protocol.SendMessage{RequestID: first, ConversationID: "main", Text: "hello"}
	turn := map[string]any{"owner": "main", "boot_id": options.BootID, "generation": 1, "request": request, "user_message_id": "original-user", "reply_message_id": "original-reply", "state": "running"}
	rawTurn, _ := json.Marshal(turn)
	completion, _ := json.Marshal(map[string]any{"turn": turn, "phase": execution.Succeeded, "error": ""})
	if err = store.Set(ctx, "conversation.turn", string(rawTurn)); err != nil {
		t.Fatal(err)
	}
	if err = store.Set(ctx, "conversation.completion", string(completion)); err != nil {
		t.Fatal(err)
	}
	queued, _ := json.Marshal(protocol.SendMessage{RequestID: next, ConversationID: "main", Text: "next"})
	if _, err = store.DB().Exec("INSERT INTO chat_user_inbox(request,message) VALUES(?,?)", string(queued), "next-user"); err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	options.BootID = "restarted"
	options.Manager, _ = execution.New(options.RuntimeID, options.BootID, execution.DefaultLimits())
	restarted, err := Open(options)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	c, err = restarted.Conversation(ctx, "main")
	if err != nil {
		t.Fatal(err)
	}
	awaitConversation(t, c, func(snapshot protocol.Snapshot) bool { return snapshot.Turn == nil && calls.Load() > 0 })
	if calls.Load() != 1 {
		t.Fatal("replayed completed request", calls.Load())
	}
}
