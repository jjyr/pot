package assistant

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestInstructionUpdateReviewCASAndDurableOriginalReceipt(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "assistant.db")
	var cleanup atomic.Int64
	c := conversationFixture(t, path, "one", nil, &cleanup)
	before, err := c.Instructions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.PrepareInstructionUpdate(ctx, "proposal", "reviewed"); err != nil {
		t.Fatal(err)
	}
	concurrent, err := c.SaveInstructions(ctx, "gui edit", before.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.CommitInstructionUpdate(ctx, "proposal"); err == nil {
		t.Fatal("review overwrote concurrent GUI edit")
	}
	if _, err = c.PrepareInstructionUpdate(ctx, "accepted", "accepted rules"); err != nil {
		t.Fatal(err)
	}
	saved, err := c.CommitInstructionUpdate(ctx, "accepted")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.SaveInstructions(ctx, "later rules", saved.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = c.PrepareInstructionUpdate(ctx, "accepted", "different"); err == nil {
		t.Fatal("request identity reused")
	}
	if err = c.Close(); err != nil {
		t.Fatal(err)
	}
	c = conversationFixture(t, path, "two", nil, &cleanup)
	receipt, err := c.PrepareInstructionUpdate(ctx, "accepted", "accepted rules")
	if err != nil || receipt.Result == nil || *receipt.Result != saved || receipt.Before.Revision != concurrent.Revision {
		t.Fatal(receipt, err)
	}
	duplicate, err := c.CommitInstructionUpdate(ctx, "accepted")
	if err != nil || duplicate != saved {
		t.Fatal(duplicate, err)
	}
	current, _ := c.Instructions(ctx)
	if current.Text != "later rules" {
		t.Fatal("retry reapplied old instruction effect")
	}
}
