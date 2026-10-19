package bridge

import (
	"context"
	"sync"
	"testing"

	"github.com/jjyr/pot/pkg/protocol"
)

func seedPeerMessage(t *testing.T, h *agentHost, id string, hops uint32, peer protocol.AgentRef) protocol.Envelope {
	t.Helper()
	e, err := h.mailbox.send(context.Background(), peer, "fixture-"+id, "reference "+id)
	if err != nil {
		t.Fatal(err)
	}
	e.ID = id
	e.From = peer
	e.To = h.identity.Address
	e.Hops = hops
	if _, err = h.mailbox.Deliver(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	return e
}
func TestCodexConsumersAtomicallyClaimAndBindCausalParent(t *testing.T) {
	ctx := context.Background()
	h, _ := testHost(t)
	defer h.close()
	peer := protocol.AgentRef{NodeID: "peer-one", Kind: "assistant", AgentID: "helper"}
	other := protocol.AgentRef{NodeID: "peer-two", Kind: "assistant", AgentID: "helper"}
	low := seedPeerMessage(t, h, "low", 2, other)
	high := seedPeerMessage(t, h, "high", 15, peer)
	type result struct {
		consumer string
		items    []protocol.MailRecord
		err      error
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for _, consumer := range []string{"consumer-a", "consumer-b"} {
		wg.Add(1)
		go func(consumer string) {
			defer wg.Done()
			items, err := h.mailbox.claim(ctx, consumer)
			results <- result{consumer, items, err}
		}(consumer)
	}
	wg.Wait()
	close(results)
	winner, loser := "", ""
	for r := range results {
		if r.err != nil {
			t.Fatal(r.err)
		}
		if len(r.items) == 2 {
			winner = r.consumer
		} else if len(r.items) == 0 {
			loser = r.consumer
		} else {
			t.Fatal(r)
		}
	}
	if winner == "" || loser == "" {
		t.Fatal("duplicate or missing claim")
	}
	if err := h.mailbox.ackConsumer(ctx, loser, high.ID); err == nil {
		t.Fatal("another consumer acknowledged the claim")
	}
	resumed, err := h.mailbox.claim(ctx, winner)
	if err != nil || len(resumed) != 2 {
		t.Fatal(resumed, err)
	}
	reply, err := h.mailbox.sendConsumer(ctx, winner, peer, "reply", "reply")
	if err != nil || reply.Hops != 16 || reply.ReplyTo == nil || *reply.ReplyTo != high.ID || reply.ConversationID != high.ConversationID {
		t.Fatal(reply, err)
	}
	forwarded, err := h.mailbox.sendConsumer(ctx, winner, other, "forward", "forward")
	if err != nil || forwarded.Hops != 16 || forwarded.ReplyTo != nil || forwarded.ConversationID == high.ConversationID {
		t.Fatal(forwarded, err)
	}
	for _, id := range []string{low.ID, high.ID} {
		if err = h.mailbox.ackConsumer(ctx, winner, id); err != nil {
			t.Fatal(err)
		}
	}
	afterAck, err := h.mailbox.sendConsumer(ctx, winner, other, "after-ack", "reference")
	if err != nil || afterAck.Hops != 16 {
		t.Fatal("ack reset causal context", afterAck, err)
	}
	seedPeerMessage(t, h, "at-limit", 16, peer)
	if _, err = h.mailbox.claim(ctx, winner); err != nil {
		t.Fatal(err)
	}
	if _, err = h.mailbox.sendConsumer(ctx, winner, other, "bypass", "forward without reply_to"); err == nil {
		t.Fatal("omitting a model reply field bypassed hop limit")
	}
	// Exact retries retain the original accepted parent even after a deeper read.
	again, err := h.mailbox.sendConsumer(ctx, winner, other, "forward", "forward")
	if err != nil || again.ID != forwarded.ID || again.Hops != forwarded.Hops {
		t.Fatal(again, err)
	}
}
func TestCodexConsumerRestartAndExplicitRecoveryFenceOldReaders(t *testing.T) {
	ctx := context.Background()
	h, root := testHost(t)
	peer := protocol.AgentRef{NodeID: "peer", Kind: "assistant", AgentID: "helper"}
	original := seedPeerMessage(t, h, "fenced", 15, peer)
	if _, err := h.mailbox.claim(ctx, "original-consumer"); err != nil {
		t.Fatal(err)
	}
	h.close()
	var err error
	h, err = openAgentHost(root)
	if err != nil {
		t.Fatal(err)
	}
	defer h.close()
	items, err := h.mailbox.claim(ctx, "new-consumer")
	if err != nil || len(items) != 0 {
		t.Fatal("restart replayed uncertain messages", items, err)
	}
	items, err = h.mailbox.claim(ctx, "original-consumer")
	if err != nil || len(items) != 1 || items[0].Message.ID != original.ID {
		t.Fatal(items, err)
	}
	if err = h.mailbox.recoverConsumers(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = h.mailbox.claim(ctx, "original-consumer"); err == nil {
		t.Fatal("recovered reader still active")
	}
	if err = h.mailbox.ackConsumer(ctx, "original-consumer", original.ID); err == nil {
		t.Fatal("old reader can ack after recovery")
	}
	if _, err = h.mailbox.sendConsumer(ctx, "original-consumer", peer, "stale-send", "late reply"); err == nil {
		t.Fatal("old reader can send after recovery")
	}
	items, err = h.mailbox.claim(ctx, "new-consumer")
	if err != nil || len(items) != 1 || items[0].Message.Hops != 15 {
		t.Fatal("recovery lost original causal facts", items, err)
	}
	reply, err := h.mailbox.sendConsumer(ctx, "new-consumer", peer, "recovered-reply", "authorized reply")
	if err != nil || reply.Hops != 16 || reply.ReplyTo == nil || *reply.ReplyTo != original.ID {
		t.Fatal(reply, err)
	}
}
