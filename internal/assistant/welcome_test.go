package assistant

import (
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"testing"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/execution"
	"github.com/jjyr/pot/pkg/protocol"
)

func TestWelcomeSurvivesRestartAndReachesModelAPI(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "conversation.sqlite")
	manager, _ := execution.New("runtime", "boot", execution.DefaultLimits())
	spec := resolved(t, "model")
	spec.Params.Agent.AutoCompact = false
	spec.Params.Agent.BuiltinTools = []string{}
	if err := spec.Rehash(); err != nil {
		t.Fatal(err)
	}
	welcome := "你好，我是 Pot。\n\n你可以给我起个名字，或者告诉我你希望我如何帮你。"
	requests := make(chan core.ModelRequest, 2)
	options := ConversationOptions{Owner: "main", RuntimeID: "runtime", BootID: "boot", Manager: manager, InitialSpec: &spec, InitialWelcome: welcome}
	options.Prepare = func(context.Context, core.Thread, core.TurnRequest) (RunOptions, error) {
		return RunOptions{Cleanup: func(context.Context, core.ExecutionContext, bool) error { return nil }, Execution: core.ExecutionContext{Owner: "main", Role: core.Main}, Services: modelPort{model: func(_ context.Context, r core.ModelRequest) (io.ReadCloser, error) {
			requests <- r
			return replySSE("好的"), nil
		}}}, nil
	}
	c, err := OpenConversation(path, options)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, _ := c.Snapshot(ctx)
	if len(snapshot.Messages) != 1 || snapshot.Messages[0].Text != welcome || snapshot.Messages[0].Role != protocol.Assistant {
		t.Fatal(snapshot)
	}
	catalogue, _ := c.Catalogue(ctx)
	if catalogue.ReplyCount != 0 {
		t.Fatal("welcome generated unread notification", catalogue)
	}
	if err = c.Close(); err != nil {
		t.Fatal(err)
	}
	options.InitialWelcome = "A different locale must not rewrite history"
	c, err = OpenConversation(path, options)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	snapshot, _ = c.Snapshot(ctx)
	if len(snapshot.Messages) != 1 || snapshot.Messages[0].Text != welcome {
		t.Fatal("welcome duplicated or rewritten", snapshot)
	}
	if _, err = c.Send(ctx, protocol.SendMessage{RequestID: "first", ConversationID: "main", Text: "以后叫你小白"}); err != nil {
		t.Fatal(err)
	}
	finished := waitSnapshot(t, c, func(s protocol.Snapshot) bool { return s.Turn == nil && len(s.Messages) > 1 })
	if len(finished.Messages) != 3 {
		t.Fatalf("turn failed: %v", c.lastError)
	}
	r := <-requests
	var body struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err = json.Unmarshal(r.Body, &body); err != nil {
		t.Fatal(err)
	}
	found := false
	for i, m := range body.Messages {
		if m.Role == "assistant" && m.Content == welcome {
			found = true
			if i+1 >= len(body.Messages) || body.Messages[i+1].Role != "user" || body.Messages[i+1].Content != "以后叫你小白" {
				t.Fatal("welcome must precede user input", string(r.Body))
			}
		}
	}
	if !found {
		t.Fatal("welcome missing from actual model API request", string(r.Body))
	}
}
