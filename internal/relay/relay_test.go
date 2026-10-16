package relay

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/jjyr/pot/internal/assistant"
	"github.com/jjyr/pot/internal/protocol"
)

type testNode struct {
	id                                              string
	mailbox                                         *assistant.Mailbox
	lostAck, offline, wrongIdentity, corruptReceipt atomic.Bool
	delivered                                       atomic.Int32
}

func newNode(t *testing.T, id string) *testNode {
	t.Helper()
	mailbox, err := assistant.OpenMailbox(filepath.Join(t.TempDir(), "mail.sqlite"), id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { mailbox.Close() })
	return &testNode{id: id, mailbox: mailbox}
}
func (n *testNode) ControlID() string { return n.id }
func (n *testNode) address() protocol.AssistantRef {
	return protocol.AssistantRef{ControlID: n.id, AssistantID: "main"}
}
func (n *testNode) Directory(context.Context) (protocol.Directory, error) {
	if n.offline.Load() {
		return protocol.Directory{}, errors.New("offline")
	}
	id := n.id
	if n.wrongIdentity.Load() {
		id = "changed"
	}
	return protocol.Directory{ControlID: id, Assistants: []protocol.MailAssistant{{Address: n.address(), Name: "Main"}}}, nil
}
func (n *testNode) Register(ctx context.Context, d protocol.Directory) error {
	return n.mailbox.Register(ctx, d)
}
func (n *testNode) Pending(ctx context.Context, target string) ([]protocol.Envelope, error) {
	return n.mailbox.Pending(ctx, target)
}
func (n *testNode) Deliver(ctx context.Context, e protocol.Envelope) (protocol.Receipt, error) {
	n.delivered.Add(1)
	receipt, err := n.mailbox.Deliver(ctx, e)
	if n.corruptReceipt.Load() {
		receipt.Message.Text = "changed"
	}
	return receipt, err
}
func (n *testNode) Acknowledge(ctx context.Context, r protocol.Receipt) error {
	if n.lostAck.Swap(false) {
		return errors.New("lost acknowledgement")
	}
	return n.mailbox.Acknowledge(ctx, r)
}
func (n *testNode) send(t *testing.T, id string, target *testNode) {
	t.Helper()
	if _, err := n.mailbox.Send(context.Background(), id, n.address(), protocol.SendMail{To: target.address(), Text: "work"}, nil); err != nil {
		t.Fatal(err)
	}
}
func TestLostAckRedeliveryAndIndependentDirections(t *testing.T) {
	ctx := context.Background()
	a, b := newNode(t, "a"), newNode(t, "b")
	a.send(t, "ab", b)
	b.send(t, "ba", a)
	a.lostAck.Store(true)
	if err := Exchange(ctx, a, b); err == nil {
		t.Fatal("expected acknowledgement failure")
	}
	for _, node := range []*testNode{a, b} {
		if next, err := node.mailbox.Next(ctx, "main"); err != nil || next == nil {
			t.Fatalf("both directions must deliver: %v", err)
		}
	}
	if err := Exchange(ctx, a, b); err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]*testNode{{a, b}, {b, a}} {
		pending, err := pair[0].mailbox.Pending(ctx, pair[1].id)
		if err != nil || len(pending) != 0 {
			t.Fatalf("pending: %v %v", pending, err)
		}
	}
	history, err := b.mailbox.History(ctx, "main", 0)
	if err != nil || len(history) != 2 {
		t.Fatalf("redelivery duplicated durable input: %+v %v", history, err)
	}
}
func TestOfflineAndIdentityMismatchNeverRedirect(t *testing.T) {
	ctx := context.Background()
	a, b := newNode(t, "a"), newNode(t, "b")
	a.send(t, "ab", b)
	b.offline.Store(true)
	if Exchange(ctx, a, b) == nil {
		t.Fatal("offline should fail")
	}
	b.offline.Store(false)
	b.wrongIdentity.Store(true)
	if Exchange(ctx, a, b) == nil {
		t.Fatal("changed identity should fail")
	}
	if b.delivered.Load() != 0 {
		t.Fatal("identity mismatch delivered work")
	}
	pending, _ := a.Pending(ctx, "b")
	if len(pending) != 1 {
		t.Fatal("pending mail lost")
	}
	b.wrongIdentity.Store(false)
	if err := Exchange(ctx, a, b); err != nil {
		t.Fatal(err)
	}
}
func TestReceiptBindsEntireEnvelope(t *testing.T) {
	ctx := context.Background()
	a, b := newNode(t, "a"), newNode(t, "b")
	a.send(t, "ab", b)
	b.corruptReceipt.Store(true)
	if Exchange(ctx, a, b) == nil {
		t.Fatal("altered receipt must fail")
	}
	pending, _ := a.Pending(ctx, "b")
	if len(pending) != 1 {
		t.Fatal("mismatched receipt acknowledged source")
	}
	b.corruptReceipt.Store(false)
	if err := Exchange(ctx, a, b); err != nil {
		t.Fatal(err)
	}
}
