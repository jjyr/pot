package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

func TestUIProjectionBoundedStreamAndFullAgentHistory(t *testing.T) {
	ctx := context.Background()
	var cleanup atomic.Int64
	requests := make(chan core.ModelRequest, 20)
	path := filepath.Join(t.TempDir(), "history.sqlite")
	model := func(_ context.Context, r core.ModelRequest) (io.ReadCloser, error) {
		requests <- r
		return replySSE("Remembered"), nil
	}
	c := conversationFixture(t, path, "boot", model, &cleanup)
	// Establish a genuine agent checkpoint with old context that won't fit in UI.
	c.Send(ctx, protocol.SendMessage{RequestID: "first", ConversationID: "main", Text: "oldest secret fixture marker"})
	waitSnapshot(t, c, func(s protocol.Snapshot) bool { return s.Turn == nil && len(s.Messages) == 2 })
	<-requests
	c.mu.Lock()
	for i := 0; i < 1000; i++ {
		err := c.putMessageLocked(ctx, protocol.Message{ID: fmt.Sprintf("historical-%04d", i), ConversationID: "main", Role: protocol.System, Text: strings.Repeat("history ", 40), Status: protocol.Complete, CreatedAtMS: 1}, nil)
		if err != nil {
			t.Fatal(err)
		}
	}
	c.mu.Unlock()
	wakes, remove, err := c.SubscribeUI(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer remove()
	<-wakes
	initial, err := c.UIFrame(ctx, 0, true)
	if err != nil || len(initial.Messages) != storage.UIPageSize || !initial.History.HasMore {
		t.Fatal(len(initial.Messages), err)
	}
	body, _ := json.Marshal(initial)
	var decoded protocol.UIFrame
	if err = protocol.DecodeStrict(body, &decoded); err != nil {
		t.Fatal("wire DTO", err)
	}
	full, _ := c.Snapshot(ctx)
	if len(full.Messages) != 1002 {
		t.Fatal("UI truncated authoritative history")
	}
	c.mu.Lock()
	c.turn = &savedTurn{UserMessageID: full.Messages[0].ID, State: "thinking"}
	c.mu.Unlock()
	active, activeErr := c.UIFrame(ctx, 0, true)
	if activeErr != nil || active.Turn == nil || len(active.Messages) > storage.UIPageSize+33 {
		t.Fatal("unbounded active turn", activeErr)
	}
	foundInput := false
	for _, message := range active.Messages {
		if message.ID == full.Messages[0].ID {
			foundInput = message.ContextOnly
		}
	}
	if !foundInput {
		t.Fatal("active input missing from bounded reset")
	}
	c.mu.Lock()
	c.turn = nil
	c.mu.Unlock()
	checkpointBefore, _ := c.store.LoadContext(ctx)
	for _, n := range []int{1, 20, 120} {
		if _, err = c.UIHistory(ctx, 0, 0, "", n); err != nil {
			t.Fatal(err)
		}
	}
	checkpointAfter, _ := c.store.LoadContext(ctx)
	if string(checkpointBefore) != string(checkpointAfter) {
		t.Fatal("UI read changed checkpoint")
	}
	// Many changes before the next read consume a single wakeup and reset once.
	c.mu.Lock()
	for i := 0; i < 300; i++ {
		m := protocol.Message{ID: "changing", ConversationID: "main", Role: protocol.Assistant, Text: fmt.Sprintf("token %d", i), Status: protocol.Complete, CreatedAtMS: 1}
		if err = c.putMessageLocked(ctx, m, nil); err != nil {
			t.Fatal(err)
		}
	}
	c.mu.Unlock()
	if len(wakes) != 1 {
		t.Fatal("unbounded consumer queue", len(wakes))
	}
	reset, err := c.UIFrame(ctx, initial.Cursor, false)
	if err != nil || reset.Kind != "reset" || len(reset.Messages) > storage.UIPageSize+33 {
		t.Fatal(reset.Kind, err)
	}
	c.mu.Lock()
	m := protocol.Message{ID: "changing", ConversationID: "main", Role: protocol.Assistant, Text: "next token", Status: protocol.Complete, CreatedAtMS: 1}
	c.putMessageLocked(ctx, m, nil)
	c.mu.Unlock()
	delta, err := c.UIFrame(ctx, reset.Cursor, false)
	data, _ := json.Marshal(delta)
	if err != nil || delta.Kind != "delta" || len(delta.Messages) != 1 || len(data) > 1800 || strings.Contains(string(data), "historical-") {
		t.Fatal("history repeated in delta", len(data), err)
	}
	// Fake provider receives the old real checkpoint despite all UI reads.
	c.Send(ctx, protocol.SendMessage{RequestID: "second", ConversationID: "main", Text: "Recall the oldest marker"})
	waitSnapshot(t, c, func(s protocol.Snapshot) bool { return s.Turn == nil && len(s.Messages) == 1005 })
	request := <-requests
	if !strings.Contains(string(request.Body), "oldest secret fixture marker") {
		t.Fatal("model lost complete existing context")
	}
	if err = c.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := conversationFixture(t, path, "next-boot", model, &cleanup)
	reopened.Send(ctx, protocol.SendMessage{RequestID: "third", ConversationID: "main", Text: "Recall it after restart"})
	waitSnapshot(t, reopened, func(s protocol.Snapshot) bool { return s.Turn == nil && len(s.Messages) == 1007 })
	if request = <-requests; !strings.Contains(string(request.Body), "oldest secret fixture marker") {
		t.Fatal("UI window changed resumed model context")
	}
}

func TestUIDeltaSizeDoesNotScaleWithHistory(t *testing.T) {
	ctx := context.Background()
	sizes := []int{}
	for _, count := range []int{10, 1500} {
		var cleanup atomic.Int64
		c := conversationFixture(t, filepath.Join(t.TempDir(), "chat.sqlite"), "boot", func(context.Context, core.ModelRequest) (io.ReadCloser, error) { return replySSE("ok"), nil }, &cleanup)
		c.mu.Lock()
		for i := 0; i < count; i++ {
			if err := c.putMessageLocked(ctx, protocol.Message{ID: fmt.Sprintf("old-%d", i), ConversationID: "main", Role: protocol.System, Text: strings.Repeat("old history ", 100), Status: protocol.Complete, CreatedAtMS: 1}, nil); err != nil {
				t.Fatal(err)
			}
		}
		before := c.cursor
		if err := c.putMessageLocked(ctx, protocol.Message{ID: "active", ConversationID: "main", Role: protocol.Assistant, Text: "one token", Status: protocol.Streaming, CreatedAtMS: 1}, nil); err != nil {
			t.Fatal(err)
		}
		c.mu.Unlock()
		frame, err := c.UIFrame(ctx, before, false)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(frame)
		sizes = append(sizes, len(data))
		if len(frame.Messages) != 1 || frame.Budget == nil || frame.Budget.Limit != 10 || frame.Agent.Phase != protocol.Ready || frame.Agent.Model != "model" {
			t.Fatal("state missing from delta", frame)
		}
	}
	if sizes[1] > sizes[0]+32 {
		t.Fatalf("delta grew with historical messages: %v", sizes)
	}
}

func TestUIReplyLifecycleSettlesOnEmptyCleanupDelta(t *testing.T) {
	ctx := context.Background()
	var cleanup atomic.Int64
	c := conversationFixture(t, filepath.Join(t.TempDir(), "chat.sqlite"), "boot", func(context.Context, core.ModelRequest) (io.ReadCloser, error) { return replySSE("ok"), nil }, &cleanup)
	c.mu.Lock()
	input := protocol.Message{ID: "input", ConversationID: "main", Role: protocol.User, Text: "hello", Status: protocol.Complete, CreatedAtMS: 1}
	if err := c.putMessageLocked(ctx, input, nil); err != nil {
		t.Fatal(err)
	}
	output := protocol.Message{ID: "output", ConversationID: "main", Role: protocol.Assistant, ReplyTo: input.ID, Text: "", Status: protocol.Complete, CreatedAtMS: 1}
	if err := c.putMessageLocked(ctx, output, nil); err != nil {
		t.Fatal(err)
	}
	c.turn = &savedTurn{UserMessageID: input.ID, State: "thinking"}
	before := c.cursor
	c.mu.Unlock()
	active, err := c.UIFrame(ctx, before, false)
	if err != nil || len(active.Replies) != 1 || active.Replies[0].State != "running" {
		t.Fatal(active, err)
	}
	c.mu.Lock()
	c.turn = nil
	c.mu.Unlock()
	settled, err := c.UIFrame(ctx, before, false)
	if err != nil || len(settled.Messages) != 0 || len(settled.Replies) != 1 || settled.Replies[0].State != "completed" {
		t.Fatal(settled, err)
	}
}
