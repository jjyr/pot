package tasks

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jjyr/pot/pkg/protocol"
)

func TestForwardedPeerMessageRemainsReferenceAndRetainsHopBound(t *testing.T) {
	ctx := context.Background()
	s, _, _, _ := fixture(t)
	record, err := s.Create(ctx, "assistant", protocol.CreateTask{RequestID: "create", Text: "work"})
	if err != nil {
		t.Fatal(err)
	}
	sender := protocol.AgentRef{ControlID: s.ControlID(), Kind: "assistant", AgentID: "assistant"}
	parent := protocol.Envelope{ID: "peer-parent", Kind: "message", From: protocol.AgentRef{ControlID: s.ControlID(), Kind: "assistant", AgentID: "peer"}, To: sender, ConversationID: record.RunID, Text: "reference", Hops: protocol.MaxMailHops}
	if err = s.SendMessageWithParent(ctx, "assistant", record.Info.ID, "bounded", sender, "forwarded data", &parent); err == nil {
		t.Fatal("Task forwarding reset the automatic hop bound")
	}
	parent.ID = "valid-parent"
	parent.Hops = 3
	if err = s.SendMessageWithParent(ctx, "assistant", record.Info.ID, "reference", sender, "forwarded data", &parent); err != nil {
		t.Fatal(err)
	}
	inputs, err := s.Inputs(ctx, "assistant", record.Info.ID)
	if err != nil || len(inputs) != 2 || inputs[1].Kind != "reference" {
		t.Fatal("peer content became owner guidance", inputs, err)
	}
	var body []byte
	if err = s.DB().QueryRow("SELECT envelope FROM agent_messages WHERE json_extract(envelope,'$.request_id')='reference'").Scan(&body); err != nil {
		t.Fatal(err)
	}
	var stored protocol.Envelope
	if err = json.Unmarshal(body, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.From != sender || stored.Hops != 4 || stored.ReplyTo == nil || *stored.ReplyTo != parent.ID {
		t.Fatal("message provenance or causation was lost", stored)
	}
}
