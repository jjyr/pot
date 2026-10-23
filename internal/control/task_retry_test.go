package control

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jjyr/pot/pkg/protocol"
)

func TestFailedTaskRecoveryIsSilentAndReplacementReportsOnce(t *testing.T) {
	t.Parallel()
	for _, failures := range []int32{1, 3} {
		t.Run(fmt.Sprintf("transient-failures=%d", failures), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			var workers, recoveryRounds, reports atomic.Int32
			s, _ := batchService(t, func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				var body struct {
					Messages []struct{ Role, Content string }
					Tools    []struct{ Function struct{ Name string } }
				}
				if err := json.Unmarshal(raw, &body); err != nil {
					t.Error(err)
					return
				}
				if strings.Contains(body.Messages[0].Content, "Process background updates") {
					hasRetry := false
					for _, tool := range body.Tools {
						hasRetry = hasRetry || tool.Function.Name == "retry_task"
					}
					if hasRetry {
						names := map[string]bool{}
						for _, tool := range body.Tools {
							names[tool.Function.Name] = true
						}
						for _, name := range []string{"exec_command", "create_task", "send_message"} {
							if !names[name] {
								t.Error("result handling lost assistant tool", name)
							}
						}
						if recoveryRounds.Add(1)%2 == 1 {
							var payload []struct {
								TaskID   string `json:"artifact_task_id"`
								Allowed  bool   `json:"retry_allowed"`
								Original string `json:"original_instructions"`
							}
							content := body.Messages[len(body.Messages)-1].Content
							start := strings.LastIndex(content, "\n[")
							if start < 0 {
								t.Error("missing notification payload")
								return
							}
							if err := json.Unmarshal([]byte(content[start+1:]), &payload); err != nil || len(payload) != 1 || !payload[0].Allowed || !strings.Contains(payload[0].Original, "Preserve original constraints") {
								t.Error("recovery lost authority context", content, err)
								return
							}
							args, _ := json.Marshal(map[string]string{"task_id": payload[0].TaskID, "instructions": "Retry the transient read failure; preserve original constraints."})
							event, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "retry-call", "type": "function", "function": map[string]any{"name": "retry_task", "arguments": string(args)}}}}, "finish_reason": "tool_calls"}}})
							w.Header().Set("Content-Type", "text/event-stream")
							fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", event)
							return
						}
						batchReply(w, "")
						return
					}
					batchReply(w, "Recovered result delivered.")
					reports.Add(1)
					return
				}
				if workers.Add(1) <= failures {
					http.Error(w, "transient read failure", http.StatusBadRequest)
					return
				}
				batchReply(w, "Recovered output.")
			}, 2)
			c, err := s.Conversation(ctx, "main")
			if err != nil {
				t.Fatal(err)
			}
			baseline, err := c.Catalogue(ctx)
			if err != nil {
				t.Fatal(err)
			}
			original, err := s.Tasks.AcceptLocal(ctx, "main", protocol.CreateTask{RequestID: "recover", Text: "Read upstream\nPreserve original constraints."})
			if err != nil {
				t.Fatal(err)
			}
			awaitConversationContext(t, ctx, c, func(snapshot protocol.Snapshot) bool { return snapshot.Turn == nil && reports.Load() == 1 })
			if workers.Load() != failures+1 || recoveryRounds.Load() != 2*failures {
				t.Fatal("unexpected recovery loop", workers.Load(), recoveryRounds.Load())
			}
			catalogue, err := c.Catalogue(ctx)
			if err != nil || catalogue.ReplyCount != baseline.ReplyCount+1 {
				t.Fatal("silent recovery generated a notification", catalogue, baseline, err)
			}
			var replacement string
			if err := s.Tasks.DB().QueryRowContext(ctx, "SELECT task_id FROM task_retries WHERE source_task=?", original.Info.ID).Scan(&replacement); err != nil {
				t.Fatal(err)
			}
			// Even another tool call from the same batch cannot target unrelated work.
			if err := s.deliveredTask(ctx, "main", "pot-result-unknown", replacement); err == nil {
				t.Fatal("unclaimed task authorized recovery")
			}
			if _, err := s.retryDeliveredTask(ctx, "main", "user-turn", json.RawMessage(`{"task_id":"unrelated","instructions":"retry"}`)); err == nil {
				t.Fatal("ordinary input invoked recovery tool")
			}
		})
	}
}

func TestCreationErrorsCanBeRepairedAndMultipleTasksDispatchSilently(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var rounds atomic.Int32
	s, _ := batchService(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body struct{ Messages []struct{ Content string } }
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Error(err)
			return
		}
		if strings.Contains(body.Messages[0].Content, "You cannot delegate.") {
			<-r.Context().Done()
			return
		}
		var briefs []string
		switch rounds.Add(1) {
		case 1:
			briefs = []string{"Missing full instructions"}
		case 2:
			if !strings.Contains(string(raw), "complete execution instructions") {
				t.Error("creation failure did not return to model")
			}
			briefs = []string{"First task\nComplete the first independent goal.", "Second task\nComplete the second independent goal."}
		default:
			batchReply(w, "")
			return
		}
		var calls []any
		for i, brief := range briefs {
			args, _ := json.Marshal(map[string]string{"text": brief})
			calls = append(calls, map[string]any{"index": i, "id": fmt.Sprintf("create-%d-%d", rounds.Load(), i), "type": "function", "function": map[string]any{"name": "create_task", "arguments": string(args)}})
		}
		event, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": calls}, "finish_reason": "tool_calls"}}})
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", event)
	}, 3)
	c, err := s.Conversation(ctx, "main")
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := c.Catalogue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Send(ctx, protocol.SendMessage{RequestID: "arrange", ConversationID: "main", Text: "Arrange two independent background tasks."}); err != nil {
		t.Fatal(err)
	}
	awaitConversation(t, c, func(snapshot protocol.Snapshot) bool { return snapshot.Turn == nil && rounds.Load() == 3 })
	var count int
	if err := s.Tasks.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM tasks WHERE assistant_id='main'").Scan(&count); err != nil || count != 2 {
		t.Fatal("dispatch stopped before all work was arranged", count, err)
	}
	catalogue, err := c.Catalogue(ctx)
	if err != nil || catalogue.ReplyCount != baseline.ReplyCount || catalogue.ReplyID != baseline.ReplyID {
		t.Fatal("silent creation generated an extra reply", catalogue, baseline, err)
	}
}
