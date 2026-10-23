package control

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/usage"
	"github.com/jjyr/pot/pkg/protocol"
)

func TestModelDisconnectRetryPreservesUnknownUsageReservation(t *testing.T) {
	var calls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		if calls.Add(1) == 1 {
			// Close an accepted stream with an incomplete HTTP body, as when
			// the upstream gateway restarts while a response is in progress.
			w.Header().Set("Content-Length", "10000")
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"discarded partial reply\"}}]}\n\n")
			w.(http.Flusher).Flush()
			return
		}
		if strings.Contains(string(body), "discarded partial reply") {
			t.Error("retry replayed incomplete output")
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"recovered\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":2}}\n\ndata: [DONE]\n\n")
	}))
	defer upstream.Close()
	ctx := context.Background()
	s := taskResourceService(t)
	if _, err := legacyMainAssistant(ctx, s, "create", "Pot"); err != nil {
		t.Fatal(err)
	}
	profile, err := s.Profiles.Save(ctx, SaveProfile{RequestID: "provider", ProfileID: "test", Name: "Test", Model: "model", Endpoint: upstream.URL, Protocol: "openai_chat", APIKey: "fixture", MaxOutputTokens: 100})
	if err != nil {
		t.Fatal(err)
	}
	spec, err := s.Profiles.Resolve(profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	spec.Params.Agent.BuiltinTools = []string{}
	spec.Params.Agent.AutoCompact = false
	if err := spec.Rehash(); err != nil {
		t.Fatal(err)
	}
	c, err := s.Conversation(ctx, "main")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Configure(ctx, spec, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.Usage.SetPolicy(ctx, usage.Policy{Period: "day", Limit: 1000000, Timezone: "UTC", AutoResume: true}); err != nil {
		t.Fatal(err)
	}
	accepted, err := c.Send(ctx, protocol.SendMessage{RequestID: "original", ConversationID: "main", Text: "finish this request"})
	if err != nil {
		t.Fatal(err)
	}
	failed := awaitConversation(t, c, func(snapshot protocol.Snapshot) bool {
		return snapshot.Turn == nil && len(snapshot.Messages) == 2 && snapshot.Messages[1].Status == protocol.Failed
	})
	if failed.Continuation == nil || failed.Continuation.UserMessageID != accepted.MessageID || failed.Continuation.RecoveryRequired {
		t.Fatal("interrupted model has no direct continuation", failed.Continuation)
	}
	frame, err := c.UIFrame(ctx, 0, true)
	if err != nil || frame.Continuation == nil || frame.Continuation.AttemptID != failed.Continuation.AttemptID {
		t.Fatal("paged UI lost the continuation boundary", frame.Continuation, err)
	}
	before, err := s.Usage.Quota(ctx, time.Now().UnixMilli())
	if err != nil || before.Reserved == 0 {
		t.Fatal(before, err)
	}
	command := ContinueMainCommand{RequestID: "retry", UserMessageID: accepted.MessageID, AttemptID: "original"}
	if _, err := s.ContinueMain(ctx, "main", command); err != nil {
		t.Fatal(err)
	}
	snapshot := awaitConversation(t, c, func(snapshot protocol.Snapshot) bool {
		return snapshot.Turn == nil && len(snapshot.Messages) > 1 && snapshot.Messages[len(snapshot.Messages)-1].Status == protocol.Complete
	})
	after, err := s.Usage.Quota(ctx, time.Now().UnixMilli())
	if err != nil || after.Reserved != before.Reserved || after.Used != 12 || calls.Load() != 2 || snapshot.Messages[len(snapshot.Messages)-1].Text != "recovered" {
		t.Fatal("retry lost the original reservation or response", before, after, calls.Load(), snapshot, err)
	}
	if snapshot.Continuation != nil {
		t.Fatal("completed input still offers continuation")
	}
	if duplicate, err := s.ContinueMain(ctx, "main", command); err != nil || !duplicate.Duplicate || calls.Load() != 2 {
		t.Fatal("continuation acknowledgment retry resampled", duplicate, calls.Load(), err)
	}
}
