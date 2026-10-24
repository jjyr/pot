package control

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/inbox"
	"github.com/jjyr/pot/internal/testutil"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

func TestMainConversationHasNoDefaultRequestCountCutoff(t *testing.T) {
	t.Parallel()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	finish := func() { once.Do(func() { close(release) }) }
	defer finish()
	s, _ := batchService(t, func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		select {
		case <-release:
			batchReply(w, "completed after request claims")
		case <-r.Context().Done():
		}
	}, 1)
	ctx, cancel := context.WithTimeout(context.Background(), testutil.Timeout(t, 20*time.Second))
	defer cancel()
	c, err := s.Conversation(ctx, "main")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Send(ctx, protocol.SendMessage{RequestID: "long-turn", ConversationID: "main", Text: "finish normally"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	thread, err := c.Thread(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Exercise the production request-count gate while the first response is held.
	// Agent continuation beyond 128 actual model rounds is covered separately.
	for i := range 130 {
		if err := c.ClaimModel(ctx, "long-turn", thread.Generation, fmt.Sprintf("claim-%d", i)); err != nil {
			t.Fatalf("request claim %d hit an implicit cutoff: %v", i+2, err)
		}
	}
	finish()
	snapshot := awaitConversation(t, c, func(snapshot protocol.Snapshot) bool {
		return snapshot.Turn == nil && len(snapshot.Messages) > 0 && snapshot.Messages[len(snapshot.Messages)-1].Text == "completed after request claims"
	})
	if snapshot.Agent.Phase != protocol.Ready || snapshot.Agent.Detail != nil {
		t.Fatal(snapshot.Agent)
	}
}

func TestAutomaticTurnsContinueBeyondFormerRequestCutoffs(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		kind   string
		rounds int32
	}{{"task_result", 10}, {"message", 130}} {
		t.Run(tc.kind, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			var calls atomic.Int32
			s, _ := batchService(t, func(w http.ResponseWriter, r *http.Request) {
				n := calls.Add(1)
				if n == tc.rounds {
					batchReply(w, "completed long automatic turn")
					return
				}
				event, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": fmt.Sprintf("inspect-%d", n), "type": "function", "function": map[string]any{"name": "list_assistants", "arguments": "{}"}}}}, "finish_reason": "tool_calls"}}})
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", event)
			}, 1)
			c, err := s.Conversation(ctx, "main")
			if err != nil {
				t.Fatal(err)
			}
			thread, err := c.Thread(ctx)
			if err != nil {
				t.Fatal(err)
			}
			// Keep this test focused on round continuation rather than compaction.
			spec := thread.Config
			spec.ContextWindow = 262144
			spec.Params.Agent.ContextWindow = spec.ContextWindow
			spec.Params.Agent.AutoCompact = false
			if err = spec.Rehash(); err != nil {
				t.Fatal(err)
			}
			if err = c.Configure(ctx, spec, thread.Generation); err != nil {
				t.Fatal(err)
			}
			message := protocol.Envelope{Kind: tc.kind, ID: "long-automatic", From: protocol.AgentRef{NodeID: "local", Kind: "assistant", AgentID: "peer"}, To: protocol.AgentRef{NodeID: "local", Kind: "assistant", AgentID: "main"}, ConversationID: "00000000-0000-4000-8000-000000000001", Text: "Complete this automatic update", Hops: 130}
			if tc.kind == "task_result" {
				message.From.Kind = "runtime"
				message.From.AgentID = "local"
				message.Hops = 0
				message.Result = &protocol.TaskResultEvent{TaskID: "finished-task", RunID: message.ConversationID, Title: "Finished task", Phase: protocol.TaskComplete, Text: "Finished", Origin: protocol.TaskOrigin{Kind: "manual"}}
			}
			sub := s.Mailbox.Changes.Subscribe()
			defer sub.Close()
			if err = storage.Transaction(ctx, s.Tasks.DB(), func(tx *sql.Tx) error {
				return inbox.EnqueueTx(ctx, tx, message, "in", "queued")
			}); err != nil {
				t.Fatal(err)
			}
			s.wakeTasks()
			for {
				var state string
				var detail *string
				if err = s.Tasks.DB().QueryRowContext(ctx, "SELECT state,detail FROM agent_messages WHERE id=?", message.ID).Scan(&state, &detail); err != nil {
					t.Fatal(err)
				}
				if state == "failed" || state == "interrupted" {
					t.Fatalf("automatic turn stopped after %d calls: %s %v", calls.Load(), state, detail)
				}
				if state == "processed" {
					break
				}
				if _, err = sub.Next(ctx); err != nil {
					t.Fatalf("automatic turn unfinished after %d calls: %v", calls.Load(), err)
				}
			}
			if calls.Load() != tc.rounds {
				t.Fatal("automatic turn did not reach its final response", calls.Load())
			}
			if tc.kind == "task_result" {
				awaitConversationContext(t, ctx, c, func(snapshot protocol.Snapshot) bool {
					for _, message := range snapshot.Messages {
						if message.Text == "completed long automatic turn" && snapshot.Turn == nil {
							return true
						}
					}
					return false
				})
			}
		})
	}
}
