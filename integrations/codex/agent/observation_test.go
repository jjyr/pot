package agent

import (
	"context"
	"encoding/json"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/protocol"
	"strings"
	"testing"
)

func TestObservationSeparatesTaskLoadChatAndCleanupFence(t *testing.T) {
	b := testBackend(t)
	ctx := context.Background()
	from := protocol.AgentRef{NodeID: "peer", Kind: "assistant", AgentID: "main"}
	running, err := b.Submit(ctx, agentlink.TaskRequest{From: from, RequestID: "running", Text: "secret task text"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.Submit(ctx, agentlink.TaskRequest{From: from, RequestID: "queued", Text: "secret queued text"}); err != nil {
		t.Fatal(err)
	}
	if _, err = b.submit(ctx, agentlink.TaskRequest{From: from, RequestID: "chat", Text: "secret chat text"}, &protocol.Envelope{ID: "private-message"}); err != nil {
		t.Fatal(err)
	}
	if err = b.update(ctx, running.ID, func(j *job) error { j.Task.State = "running"; return nil }); err != nil {
		t.Fatal(err)
	}
	value, err := b.Observe(ctx, "peer")
	if err != nil {
		t.Fatal(err)
	}
	a := value.Assistants[0]
	if a.Running != 1 || a.Queued != 1 || a.ChatState != "queued" || a.Fenced {
		t.Fatalf("incorrect task/chat split: %+v", a)
	}
	if err = b.update(ctx, running.ID, func(j *job) error { j.Task.State = "failed"; j.CleanupUncertain = true; return nil }); err != nil {
		t.Fatal(err)
	}
	value, err = b.Observe(ctx, "peer")
	if err != nil {
		t.Fatal(err)
	}
	if !value.Assistants[0].Fenced || value.Assistants[0].Running != 0 {
		t.Fatal(value)
	}
	raw, _ := json.Marshal(value)
	for _, secret := range []string{"secret task", "secret chat", "private-message", b.binary} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("observation leaked execution payload")
		}
	}
}
