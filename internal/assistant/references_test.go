package assistant

import (
	"context"
	"encoding/json"
	"github.com/jjyr/pot/pkg/agent"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/modelprotocol"
	"github.com/jjyr/pot/pkg/protocol"
	"io"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"unicode/utf8"
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
	if !strings.Contains(all, `"quote":"项目 A 为 73"`) || !strings.Contains(all, `"context":"原文里有两次重复词：预算。项目 A 为 73，项目 B 为 29。"`) {
		t.Fatal("model lost selected quote or source context", all)
	}
	for _, id := range []string{first.MessageID, source.ID, receipt.MessageID} {
		if strings.Contains(all, id) {
			t.Fatal("model input contains internal message identity", all)
		}
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
	raw, err := c.store.LoadContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var checkpoint agent.Checkpoint
	json.Unmarshal(raw, &checkpoint)
	if checkpoint.Messages[len(checkpoint.Messages)-2].References[0].MessageID != source.ID {
		t.Fatal("checkpoint lost refs")
	}
	if strings.Contains(string(raw), "quoted_messages") || strings.Contains(string(raw), "ReferenceContents") {
		t.Fatal("expanded model input entered canonical checkpoint")
	}
	// Retain only a summary in model history. The original remains in the
	// transcript, so a reference can still resolve after compaction and restart.
	checkpoint.Messages = []modelprotocol.Message{modelprotocol.TextMessage("user", "Earlier plans have been summarized.")}
	compacted, _ := json.Marshal(checkpoint)
	if err := c.store.SaveContext(ctx, compacted); err != nil {
		t.Fatal(err)
	}
	if err = c.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := conversationFixture(t, path, "new-boot", func(_ context.Context, r core.ModelRequest) (io.ReadCloser, error) {
		requests <- r
		return replySSE("项目 B 为 29。"), nil
	}, &cleanup)
	restored, err := reopened.Snapshot(ctx)
	if err != nil || len(restored.Messages) != 4 || restored.Messages[2].ID != receipt.MessageID || restored.Messages[2].References[0].Quote != "项目 A 为 73" {
		t.Fatal(restored, err)
	}
	if _, err := reopened.Send(ctx, protocol.SendMessage{RequestID: "third", ConversationID: "main", Text: "原消息里项目 B 是多少？", References: []core.MessageReference{{MessageID: source.ID}}}); err != nil {
		t.Fatal(err)
	}
	waitSnapshot(t, reopened, func(s protocol.Snapshot) bool { return s.Turn == nil && len(s.Messages) == 6 })
	actual = <-requests
	if strings.Contains(string(actual.Body), source.ID) || !strings.Contains(string(actual.Body), "项目 B 为 29") || !strings.Contains(string(actual.Body), "quoted_messages") {
		t.Fatal("compacted reference did not resolve directly from transcript", string(actual.Body))
	}
	if err := core.ValidateMessageReferences([]core.MessageReference{{MessageID: "source", Quote: strings.Repeat("中", 1366)}}); err == nil {
		t.Fatal("UTF-8 quote overflow accepted")
	}
}

func TestReferenceContextIsBoundedAndPreservesSelection(t *testing.T) {
	selected := "末尾选中的🙂片段"
	source := strings.Repeat("中文🙂", referenceContextBytes) + selected + strings.Repeat("后文", 100)
	text, truncated := referenceContext(source, selected)
	if !truncated || len(text) > referenceContextBytes || !utf8.ValidString(text) || !strings.Contains(text, selected) {
		t.Fatal("selection lost or invalid bounded context", len(text), truncated)
	}
	text, truncated = referenceContext(source, "rendered Markdown selection")
	if !truncated || len(text) > referenceContextBytes || !utf8.ValidString(text) || !strings.HasPrefix(source, text) {
		t.Fatal("unmatched selection did not retain bounded source context")
	}
}
