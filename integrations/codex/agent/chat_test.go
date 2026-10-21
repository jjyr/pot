package agent

import (
	"context"
	"errors"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/protocol"
	"testing"
)

func TestChatStatusTracksReceiverOutcomeIndependentlyOfReplyText(t *testing.T) {
	b := testBackend(t)
	ctx := context.Background()
	a, _ := b.resolve(ctx, "codex")
	parent := protocol.Envelope{Kind: "message", ID: "chat-input", From: protocol.AgentRef{NodeID: "pot", Kind: "user", AgentID: "user-session"}, To: originalIdentity(b, a), ConversationID: "123e4567-e89b-42d3-a456-426614174000", Text: "hello"}
	mail := hostMailbox{db: b.db, local: parent.To, backend: b}
	if _, err := mail.Deliver(ctx, parent); err != nil {
		t.Fatal(err)
	}
	if err := b.receiveMessages(ctx); err != nil {
		t.Fatal(err)
	}
	query := agentlink.ChatLookup{From: parent.From, AssistantID: "codex", MessageID: parent.ID}
	id := jobID(parent.From, "mail:"+parent.ID)
	for _, state := range []string{"queued", "running", "cancelling", "completed", "failed", "cancelled"} {
		if err := b.update(ctx, id, func(j *job) error {
			j.Task.State = state
			j.Task.Error = ""
			if state == "failed" {
				j.Task.Error = "codex_turn_failed"
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		status, err := b.ChatStatus(ctx, query)
		wantMessages := 0
		if state == "failed" || state == "cancelled" {
			wantMessages = 1
		}
		if err != nil || status.State != state || len(status.Messages) != wantMessages {
			t.Fatal(status, err)
		}
		if state == "failed" && status.Error != "codex_turn_failed" {
			t.Fatal(status)
		}
	}
	if err := b.update(ctx, id, func(j *job) error { j.Task.State = "completed"; j.Task.Result = "answer"; return nil }); err != nil {
		t.Fatal(err)
	}
	status, err := b.ChatStatus(ctx, query)
	if err != nil || len(status.Messages) != 1 || status.Messages[0].ReplyTo == nil || *status.Messages[0].ReplyTo != parent.ID || status.Messages[0].Text != "answer" {
		t.Fatal(status, err)
	}
	// A lost publisher wakeup cannot drop the result; publishing later is identical.
	if err := b.publishMessages(ctx); err != nil {
		t.Fatal(err)
	}
	repeated, err := b.ChatStatus(ctx, query)
	if err != nil || len(repeated.Messages) != 1 || repeated.Messages[0].ID != status.Messages[0].ID {
		t.Fatal(repeated, err)
	}
	wrong := query
	wrong.From.AgentID = "other-session"
	if _, err := b.ChatStatus(ctx, wrong); !errors.Is(err, agentlink.ErrUnauthorized) {
		t.Fatal("read another user's result", err)
	}
	if err := b.update(ctx, id, func(j *job) error { j.Task.State = "failed"; j.CleanupUncertain = true; return nil }); err != nil {
		t.Fatal(err)
	}
	status, err = b.ChatStatus(ctx, query)
	if err != nil || status.State != "unknown" {
		t.Fatal(status, err)
	}
}
