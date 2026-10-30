package assistant

import (
	"encoding/json"
	"fmt"
	"github.com/jjyr/pot/pkg/agent"
	"strings"
	"testing"
	"time"
)

func TestCommunicationUsageDedupBatchPaginationAndPrivacy(t *testing.T) {
	m, _ := mailbox(t, "local")
	ctx := t.Context()
	from, to := address("remote", "sender"), address("local", "owner")
	first := sendMail(t, m, "seed", address("local", "sender"), to, nil)
	// Local delivery is one logical message even though both endpoints are local.
	sendMail(t, m, "seed", address("local", "sender"), to, nil)
	second := first
	second.ID = "second"
	second.ConversationID = newConversation()
	second.From = from
	second.Text = "秘密🙂"
	deliver(t, m, second)
	deliver(t, m, second)
	third := second
	third.ID = "third"
	third.ConversationID = newConversation()
	deliver(t, m, third)
	_, err := m.ClaimMailBatch(ctx, second)
	if err != nil {
		t.Fatal(err)
	}
	got, err := m.CommunicationUsage(ctx, "owner", 1, time.Now().UnixMilli()+1, 0)
	if err != nil || got.Count != 3 || len(got.Items) != 3 {
		t.Fatal(got, err)
	}
	for _, item := range got.Items {
		if item.ID == "second" || item.ID == "third" {
			if item.ContentEstimatedTokens == nil || *item.ContentEstimatedTokens != int64(agent.EstimateTextTokens(second.Text)) || item.EstimateMethod == nil || *item.EstimateMethod != agent.ContentEstimateMethod {
				t.Fatal(item)
			}
		}
	}
	raw, _ := json.Marshal(got)
	var fields map[string]any
	if json.Unmarshal(raw, &fields) != nil {
		t.Fatal(string(raw))
	}
	if strings.Contains(string(raw), "秘密") {
		t.Fatal("message body exposed")
	}
	if !got.EstimatesKnown || got.ContentEstimatedTokens != int64(agent.EstimateTextTokens(first.Text)+2*agent.EstimateTextTokens(second.Text)) {
		t.Fatal(got)
	}
	for i := range 21 {
		msg := second
		msg.ID = fmt.Sprintf("extra-%02d", i)
		msg.ConversationID = newConversation()
		deliver(t, m, msg)
	}
	got, err = m.CommunicationUsage(ctx, "owner", 1, time.Now().UnixMilli()+1, 0)
	if err != nil || got.Count != 24 || len(got.Items) != 20 || got.NextOffset == nil || *got.NextOffset != 20 {
		t.Fatal(got, err)
	}
	empty, err := m.CommunicationUsage(ctx, "unrelated", 1, time.Now().UnixMilli()+1, 0)
	if err != nil || empty.Count != 0 {
		t.Fatal(empty, err)
	}
}
