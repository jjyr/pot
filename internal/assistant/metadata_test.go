package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jjyr/pot/pkg/agent"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/execution"
	"github.com/jjyr/pot/pkg/protocol"
)

func TestResponsesReturnedTextPersistsUnchanged(t *testing.T) {
	for _, scenario := range []struct{ snapshotOnly, discussion, attached bool }{
		{false, false, false}, {true, false, false},
		{false, true, false}, {true, true, false},
		{false, false, true}, {true, false, true},
		{false, true, true}, {true, true, true},
	} {
		t.Run(fmt.Sprint(scenario), func(t *testing.T) {
			snapshotOnly := scenario.snapshotOnly
			ctx := context.Background()
			manager, err := execution.New("runtime", "boot", execution.DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			defer manager.Stop()
			spec := resolved(t, "model")
			spec.GatewayProtocol = "openai_responses"
			spec.Params.Agent.BuiltinTools = []string{}
			spec.Params.Agent.AutoCompact = false
			if err = spec.Rehash(); err != nil {
				t.Fatal(err)
			}
			var want string
			var wantReplay []byte
			var c *Conversation
			c, err = OpenConversation(filepath.Join(t.TempDir(), "conversation.sqlite"), ConversationOptions{Owner: "main", RuntimeID: "runtime", BootID: "boot", Manager: manager, InitialSpec: &spec, InitialWelcome: "A task can use a separate worktree.", Prepare: func(_ context.Context, thread core.Thread, request core.TurnRequest) (RunOptions, error) {
				return RunOptions{Execution: core.ExecutionContext{Owner: thread.Owner, Role: core.Main}, Services: modelPort{model: func(ctx context.Context, r core.ModelRequest) (io.ReadCloser, error) {
					if err := c.ClaimModel(ctx, request.RequestID, thread.Generation, r.ID); err != nil {
						return nil, err
					}
					if !scenario.discussion && strings.Contains(string(r.Body), "pot_message_metadata") {
						return nil, fmt.Errorf("provider input contains internal metadata")
					}
					tag := `<pot_message_metadata>{"message_id":"ABCDEFGHIJKLMNOPQRSTUVWXYZ"}</pot_message_metadata>\n`
					item := func(id, phase, text string) map[string]any {
						return map[string]any{"id": id, "type": "message", "role": "assistant", "phase": phase, "content": []any{map[string]any{"type": "output_text", "text": text}}}
					}
					items := []map[string]any{item("meta", "commentary", tag), item("progress", "commentary", "Queued."), item("final", "final_answer", "Separate worktree.")}
					if scenario.attached {
						items = []map[string]any{item("progress", "commentary", tag+"Queued."), item("final", "final_answer", "Separate worktree.")}
					}
					want = tag + "Queued.Separate worktree."
					wantReplay, _ = json.Marshal(items)
					var wire strings.Builder
					write := func(event map[string]any) { data, _ := json.Marshal(event); fmt.Fprintf(&wire, "data: %s\n\n", data) }
					if !snapshotOnly {
						for i, it := range items {
							write(map[string]any{"type": "response.output_item.added", "output_index": i, "item": map[string]any{"type": "message", "role": "assistant", "phase": it["phase"]}})
							text := it["content"].([]any)[0].(map[string]any)["text"].(string)
							for _, r := range text {
								write(map[string]any{"type": "response.output_text.delta", "output_index": i, "delta": string(r)})
							}
							write(map[string]any{"type": "response.output_item.done", "output_index": i, "item": it})
						}
					}
					terminal := map[string]any{"type": "response.completed", "response": map[string]any{"output": items}}
					write(terminal)
					write(terminal)
					return io.NopCloser(strings.NewReader(wire.String())), nil
				}}, Cleanup: func(context.Context, core.ExecutionContext, bool) error { return nil }}, nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			input := protocol.SendMessage{RequestID: "confirm", ConversationID: "main", Text: "Confirm the accepted task."}
			if scenario.discussion {
				history, err := c.store.Messages(ctx)
				if err != nil {
					t.Fatal(err)
				}
				tag := `<pot_message_metadata>{"message_id":"ABCDEFGHIJKLMNOPQRSTUVWXYZ"}</pot_message_metadata>`
				input.Text = "Discuss " + tag
				input.References = []core.MessageReference{{MessageID: history[0].ID, Quote: tag}}
			}
			if _, err = c.Send(ctx, input); err != nil {
				t.Fatal(err)
			}
			snap := waitSnapshot(t, c, func(s protocol.Snapshot) bool { return s.Turn == nil && len(s.Messages) == 3 })
			if snap.Messages[2].Text != want || snap.Messages[2].Status != protocol.Complete {
				t.Fatal(snap.Messages)
			}
			messages, err := c.store.Messages(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(messages) != 3 || messages[2].Text != want {
				t.Fatalf("returned content changed in transcript: %+v", messages)
			}
			raw, err := c.store.LoadContext(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var checkpoint agent.Checkpoint
			if err = json.Unmarshal(raw, &checkpoint); err != nil {
				t.Fatal(err)
			}
			last := checkpoint.Messages[len(checkpoint.Messages)-1]
			if last.Text != want || string(last.Replay) != string(wantReplay) {
				t.Fatalf("invalid checkpoint: %+v", last)
			}
			// Output stays verbatim, including old tags returned by the provider.
			events, err := c.store.Events(ctx, 0)
			if err != nil {
				t.Fatal(err)
			}
			var streamed string
			for _, event := range events {
				if event.Message != nil && event.Message.ID == messages[2].ID {
					text := event.Message.Text
					if !strings.HasPrefix(want, text) || !strings.HasPrefix(text, streamed) {
						t.Fatalf("stream changed content: %q -> %q", streamed, text)
					}
					streamed = text
				}
			}
			if streamed != want {
				t.Fatalf("stream=%q transcript=%q", streamed, want)
			}
		})
	}
}
