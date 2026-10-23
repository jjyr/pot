package control

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/assistant"
	"github.com/jjyr/pot/pkg/protocol"
)

func retirementMail(s *Service) protocol.Envelope {
	return protocol.Envelope{ID: "retirement-mail", Kind: "message", From: protocol.AgentRef{NodeID: "peer", Kind: "assistant", AgentID: "sender"}, To: protocol.AgentRef{NodeID: s.options.RuntimeID, Kind: "assistant", AgentID: "main"}, ConversationID: "c0b67cb3-c5e8-40d5-8a50-881e7be02dde", Text: "Retain this accepted input."}
}

func requireRetirementCode(t *testing.T, err error, code string) {
	t.Helper()
	var api protocol.APIError
	if !errors.As(err, &api) || api.Code != code {
		t.Fatalf("got %v, want %s", err, code)
	}
}

func TestAssistantRetirementBlocksPendingMailAndRetainsReceipts(t *testing.T) {
	for _, state := range []string{"queued", "processing", "waiting", "interrupted", "processed", "failed", "suppressed"} {
		t.Run(state, func(t *testing.T) {
			b, other := linkBackendFixture(t)
			s, ctx := b.s, t.Context()
			message := retirementMail(s)
			mail := controlLinkMailbox{s}
			if _, err := mail.Deliver(ctx, message); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Tasks.DB().ExecContext(ctx, "UPDATE agent_messages SET state=? WHERE id=?", state, message.ID); err != nil {
				t.Fatal(err)
			}
			// Mail for this owner does not block an unrelated assistant's retirement.
			if err := s.DeleteAssistant(ctx, other, currentAssistantRevision(t, s, other)); err != nil {
				t.Fatal(err)
			}
			revision := currentAssistantRevision(t, s, "main")
			err := s.DeleteAssistant(ctx, "main", revision)
			if state == "queued" || state == "processing" || state == "waiting" || state == "interrupted" {
				requireRetirementCode(t, err, "assistant_busy")
				if _, err = s.Tasks.DB().ExecContext(ctx, "UPDATE agent_messages SET state='processed' WHERE id=?", message.ID); err != nil {
					t.Fatal(err)
				}
				err = s.DeleteAssistant(ctx, "main", revision)
			}
			if err != nil {
				t.Fatal(err)
			}
			// Lost acknowledgements keep their original receipt after retirement.
			if receipt, err := mail.Deliver(ctx, message); err != nil || receipt.Message != message {
				t.Fatal("accepted receipt lost after retirement", receipt, err)
			}
			changed := message
			changed.Text = "Conflicting retry"
			if _, err := mail.Deliver(ctx, changed); err == nil {
				t.Fatal("conflicting receipt accepted")
			}
			message.ID = "late-mail"
			message.ConversationID = "0db29e50-0bea-4928-aa98-c4b18c547e8f"
			_, err = mail.Deliver(ctx, message)
			requireRetirementCode(t, err, "assistant_unavailable")
			if _, err := s.Mailbox.Send(ctx, "late-local-mail", protocol.AgentRef{NodeID: s.options.RuntimeID, Kind: "assistant", AgentID: "sender"}, protocol.SendMail{To: message.To, Text: "Local input"}, nil); err == nil {
				t.Fatal("retired recipient accepted local mail")
			}
		})
	}
}

func TestAssistantRetirementSerializesMailboxAdmission(t *testing.T) {
	t.Run("mail first", func(t *testing.T) {
		b, _ := linkBackendFixture(t)
		s := b.s
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		entered, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		unblock := func() { once.Do(func() { close(release) }) }
		defer unblock()
		// Pause after validating the recipient, while the actual inbox writer
		// transaction is still held. Deletion must wait and then see this mail.
		mail, err := assistant.OpenMailbox(filepath.Join(s.options.Root, "tasks.sqlite"), s.options.RuntimeID, assistant.MailboxOptions{OwnerAvailable: func(ctx context.Context, owner string) error {
			if err := s.assistantAvailable(ctx, owner); err != nil {
				return err
			}
			close(entered)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { unblock(); mail.Close() }()
		delivered := make(chan error, 1)
		go func() { _, err := mail.Deliver(ctx, retirementMail(s)); delivered <- err }()
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		revision := currentAssistantRevision(t, s, "main")
		deleted := make(chan error, 1)
		go func() { deleted <- s.DeleteAssistant(ctx, "main", revision) }()
		// The maintenance fixture has no dispatcher. A borrowed connection here
		// proves retirement has reached the competing writer transaction.
		for s.Tasks.DB().Stats().InUse == 0 {
			select {
			case err := <-deleted:
				t.Fatalf("retirement escaped the inbox transaction: %v", err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(time.Millisecond):
			}
		}
		unblock()
		if err := <-delivered; err != nil {
			t.Fatal(err)
		}
		requireRetirementCode(t, <-deleted, "assistant_busy")
	})

	t.Run("deletion first", func(t *testing.T) {
		b, _ := linkBackendFixture(t)
		s := b.s
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		entered, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		unblock := func() { once.Do(func() { close(release) }) }
		defer unblock()
		revision := currentAssistantRevision(t, s, "main")
		deleted := make(chan error, 1)
		go func() {
			deleted <- s.Tasks.RetireOwner(ctx, "main", func() error {
				close(entered)
				select {
				case <-release:
					return s.Registry.Delete(ctx, "main", revision)
				case <-ctx.Done():
					return ctx.Err()
				}
			})
		}()
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		delivered := make(chan error, 1)
		go func() { _, err := (controlLinkMailbox{s}).Deliver(ctx, retirementMail(s)); delivered <- err }()
		unblock()
		if err := <-deleted; err != nil {
			t.Fatal(err)
		}
		requireRetirementCode(t, <-delivered, "assistant_unavailable")
		var count int
		if err := s.Tasks.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM agent_messages WHERE id=?", retirementMail(s).ID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatal("retired assistant retained new mail", count)
		}
	})
}
