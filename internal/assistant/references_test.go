package assistant

import (
	"context"
	"encoding/json"
	"github.com/jjyr/pot/pkg/agent"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
	"io"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestMessageReferencesIdentityValidationAndRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "conversation.sqlite")
	var cleanup atomic.Int64
	requests := make(chan core.ModelRequest, 10)
	c := conversationFixture(t, path, "boot", func(_ context.Context, r core.ModelRequest) (io.ReadCloser, error) {
		requests <- r
		return replySSE("原文里有两次重复词：预算。项目 A 为 73，项目 B 为 29。"), nil
	}, &cleanup)
	first, err := c.Send(ctx, protocol.SendMessage{RequestID: "first", ConversationID: "main", Text: "提供两种方案"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := waitSnapshot(t, c, func(s protocol.Snapshot) bool { return s.Turn == nil && len(s.Messages) == 2 })
	<-requests
	source := snapshot.Messages[1]
	req := protocol.SendMessage{RequestID: "second", ConversationID: "main", Text: "为什么这样选？", References: []core.MessageReference{{MessageID: source.ID, Quote: "项目 A 为 73"}}}
	receipt, err := c.Send(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	waitSnapshot(t, c, func(s protocol.Snapshot) bool { return s.Turn == nil && len(s.Messages) == 4 })
	actual := <-requests
	var body struct {
		Messages []struct {
			Content string `json:"content"`
		}
	}
	json.Unmarshal(actual.Body, &body)
	all := ""
	for _, m := range body.Messages {
		all += m.Content
	}
	if !strings.Contains(all, `"message_id":"`+first.MessageID+`"`) || !strings.Contains(all, `"message_id":"`+source.ID+`"`) || !strings.Contains(all, `"quote":"项目 A 为 73"`) {
		t.Fatal("model lost public identity or quote", all)
	}
	duplicate, err := c.Send(ctx, req)
	if err != nil || !duplicate.Duplicate || duplicate.MessageID != receipt.MessageID {
		t.Fatal(duplicate, err)
	}
	req.References[0].Quote = "项目 B 为 29"
	if _, err = c.Send(ctx, req); err == nil {
		t.Fatal("receipt reused with changed quote")
	}
	req.RequestID = "foreign"
	req.References[0].MessageID = "another-conversation"
	if _, err = c.Send(ctx, req); err == nil {
		t.Fatal("foreign message accepted")
	}
	req.RequestID = "tool"
	req.References[0].MessageID = "missing"
	if _, err = c.Send(ctx, req); err == nil {
		t.Fatal("missing message accepted")
	}
	if _, err = c.ReadMessages(ctx, []string{"missing"}); err == nil {
		t.Fatal("reader leaked unavailable message")
	}
	raw, err := c.store.LoadContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var checkpoint agent.Checkpoint
	json.Unmarshal(raw, &checkpoint)
	if checkpoint.Messages[len(checkpoint.Messages)-2].References[0].MessageID != source.ID {
		t.Fatal("checkpoint lost refs")
	}
	if err = c.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := conversationFixture(t, path, "new-boot", func(context.Context, core.ModelRequest) (io.ReadCloser, error) {
		t.Fatal("restart made model request")
		return nil, nil
	}, &cleanup)
	messages, err := reopened.ReadMessages(ctx, []string{receipt.MessageID, source.ID})
	if err != nil || len(messages) != 2 || messages[0].References[0].Quote != "项目 A 为 73" {
		t.Fatal(messages, err)
	}
	if err := core.ValidateMessageReferences([]core.MessageReference{{MessageID: "source", Quote: strings.Repeat("中", 1366)}}); err == nil {
		t.Fatal("UTF-8 quote overflow accepted")
	}
}
