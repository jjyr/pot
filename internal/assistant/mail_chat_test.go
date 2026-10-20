package assistant

import (
	"context"
	"github.com/jjyr/pot/pkg/protocol"
	"reflect"
	"testing"
)

func TestDirectChatDeliveryIsNeverEligibleForAgentProcessing(t *testing.T) {
	ctx := context.Background()
	sender, _ := mailbox(t, "remote")
	receiver, _ := mailbox(t, "local")
	value, e := sender.Send(ctx, "direct-chat", protocol.AgentRef{NodeID: "remote", Kind: "assistant", AgentID: "peer"}, protocol.SendMail{To: protocol.AgentRef{NodeID: "local", Kind: "assistant", AgentID: "main"}, Text: "direct reply"}, nil)
	if e != nil {
		t.Fatal(e)
	}
	for range 2 {
		receipt, e := receiver.DeliverChat(ctx, value)
		if e != nil || !reflect.DeepEqual(receipt.Message, value) {
			t.Fatal(receipt, e)
		}
		candidates, e := receiver.Candidates(ctx, "main")
		if e != nil || len(candidates) != 0 {
			t.Fatal("direct chat raced agent consumer", candidates, e)
		}
	}
	history, e := receiver.History(ctx, "main", 0)
	if e != nil || len(history) != 1 || history[0].State != "processed" {
		t.Fatal(history, e)
	}
	value.Text = "conflict"
	if _, e = receiver.DeliverChat(ctx, value); e == nil {
		t.Fatal("conflicting receipt admitted")
	}
}
