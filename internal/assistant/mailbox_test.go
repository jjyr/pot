package assistant

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/jjyr/pot/pkg/protocol"
)

func address(control, assistant string) protocol.AgentRef {
	return protocol.AgentRef{ControlID: control, Kind: "assistant", AgentID: assistant}
}
func mailbox(t *testing.T, control string) (*Mailbox, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mail.sqlite")
	m, err := OpenMailbox(path, control)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	return m, path
}
func sendMail(t *testing.T, m *Mailbox, id string, from, to protocol.AgentRef, reply *string, parent *protocol.Envelope) protocol.Envelope {
	t.Helper()
	message, err := m.Send(context.Background(), id, from, protocol.SendMail{To: to, Text: "Please investigate", ReplyTo: reply}, parent)
	if err != nil {
		t.Fatal(err)
	}
	return message
}
func deliver(t *testing.T, m *Mailbox, message protocol.Envelope) protocol.Receipt {
	t.Helper()
	receipt, err := m.Deliver(context.Background(), message)
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}
func TestRemoteMailDurableReceiptDedupAndRestartFence(t *testing.T) {
	ctx := context.Background()
	a, _ := mailbox(t, "control-a")
	b, bpath := mailbox(t, "control-b")
	from, to := address("control-a", "alice"), address("control-b", "bob")
	message := sendMail(t, a, "mail-1", from, to, nil, nil)
	receipt := deliver(t, b, message)
	deliver(t, b, message)
	pending, err := a.Pending(ctx, "control-b")
	if err != nil || len(pending) != 1 {
		t.Fatal(pending, err)
	}
	tampered := receipt
	tampered.Message.Text = "changed"
	if err := a.Acknowledge(ctx, tampered); err == nil {
		t.Fatal("forged receipt acknowledged")
	}
	if err := a.Acknowledge(ctx, receipt); err != nil {
		t.Fatal(err)
	}
	pending, err = a.Pending(ctx, "control-b")
	if err != nil || len(pending) != 0 {
		t.Fatal(pending, err)
	}
	next, err := b.Next(ctx, "bob")
	if err != nil || next == nil || !sameEnvelope(*next, message) {
		t.Fatal(next, err)
	}
	if err := b.Claim(ctx, message.ID); err != nil {
		t.Fatal(err)
	}
	b.Close()
	b, err = OpenMailbox(bpath, "control-b")
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	deliver(t, b, message)
	if next, err := b.Next(ctx, "bob"); err != nil || next != nil {
		t.Fatal("uncertain message replayed", next, err)
	}
	history, err := b.History(ctx, "bob", 0)
	if err != nil || len(history) != 1 || history[0].State != "interrupted" {
		t.Fatal(history, err)
	}
	if history, err := b.History(ctx, "unrelated", 0); err != nil || len(history) != 0 {
		t.Fatal("mail leaked across owners", history, err)
	}
	if wrong, err := OpenMailbox(bpath, "wrong-control"); err == nil {
		wrong.Close()
		t.Fatal("mailbox rebound")
	}
}
func TestConversationFenceDoesNotBlockOtherConversation(t *testing.T) {
	ctx := context.Background()
	a, _ := mailbox(t, "control-a")
	b, bpath := mailbox(t, "control-b")
	alice, bob := address("control-a", "alice"), address("control-b", "bob")
	one := sendMail(t, a, "one", alice, bob, nil, nil)
	deliver(t, b, one)
	if err := b.Claim(ctx, "one"); err != nil {
		t.Fatal(err)
	}
	reply := sendMail(t, b, "reply", bob, alice, &one.ID, &one)
	deliver(t, a, reply)
	two := sendMail(t, a, "two", alice, bob, &reply.ID, nil)
	deliver(t, b, two)
	b.Close()
	var err error
	b, err = OpenMailbox(bpath, "control-b")
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if next, err := b.Next(ctx, "bob"); err != nil || next != nil {
		t.Fatal("same conversation escaped fence", next, err)
	}
	independent := sendMail(t, a, "other-conversation", alice, bob, nil, nil)
	deliver(t, b, independent)
	next, err := b.Next(ctx, "bob")
	if err != nil || next == nil || next.ID != independent.ID {
		t.Fatal("another conversation blocked", next, err)
	}
}
func TestLocalMailReplyParticipantsAndAutomaticHopBound(t *testing.T) {
	ctx := context.Background()
	m, _ := mailbox(t, "local")
	alice, bob, charlie := address("local", "alice"), address("local", "bob"), address("local", "charlie")
	initial := sendMail(t, m, "one", alice, bob, nil, nil)
	if pending, err := m.Pending(ctx, "local"); err != nil || len(pending) != 0 {
		t.Fatal(pending, err)
	}
	if _, err := m.Send(ctx, "wrong-reply", charlie, protocol.SendMail{To: alice, Text: "wrong", ReplyTo: &initial.ID}, nil); err == nil {
		t.Fatal("reply participants changed")
	}
	reply := sendMail(t, m, "reply", bob, alice, &initial.ID, &initial)
	if reply.ConversationID != initial.ConversationID || reply.Hops != 1 {
		t.Fatal(reply)
	}
	if err := m.Claim(ctx, "one"); err != nil {
		t.Fatal(err)
	}
	if err := m.Finish(ctx, "one", true, "done"); err != nil {
		t.Fatal(err)
	}
	if err := m.Finish(ctx, "one", true, "done"); err != nil {
		t.Fatal("repeat completion failed", err)
	}
	if _, err := m.Send(ctx, "one", alice, protocol.SendMail{To: bob, Text: "changed"}, nil); err == nil {
		t.Fatal("conflicting message ID accepted")
	}
	remote := protocol.Envelope{Kind: "message", ID: "hop-limit", From: address("remote", "peer"), To: bob, ConversationID: newConversation(), Text: "last allowed hop", Hops: protocol.MaxMailHops}
	deliver(t, m, remote)
	if _, err := m.Send(ctx, "escape", bob, protocol.SendMail{To: alice, Text: "new conversation"}, &remote); err == nil {
		t.Fatal("new conversation escaped automatic hop bound")
	}
	history, err := m.Conversation(ctx, "charlie", initial.ConversationID)
	if err != nil || len(history) != 0 {
		t.Fatal("conversation leaked", history, err)
	}
}
func TestPeerDirectoryValidatesIdentityAndNames(t *testing.T) {
	ctx := context.Background()
	m, _ := mailbox(t, "local")
	directory := protocol.Directory{ControlID: "remote", Assistants: []protocol.MailAssistant{{Address: address("remote", "alice"), Name: "研究"}}}
	if err := m.Register(ctx, directory); err != nil {
		t.Fatal(err)
	}
	contacts, err := m.Contacts(ctx)
	if err != nil || len(contacts) != 1 || contacts[0].Name != "研究" {
		t.Fatal(contacts, err)
	}
	directory.Assistants = append(directory.Assistants, directory.Assistants[0])
	if err := m.Register(ctx, directory); err == nil {
		t.Fatal("duplicate peer address accepted")
	}
}
