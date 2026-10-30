package control

import (
	"context"
	"encoding/json"
	"github.com/jjyr/pot/pkg/protocol"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestExperimentRetryRetainsMainAndTaskEfforts(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	var workers, main atomic.Int32
	s, _ := batchService(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body struct {
			Messages []struct{ Role, Content string }
			Effort   string `json:"reasoning_effort"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Error(err)
			return
		}
		last := body.Messages[len(body.Messages)-1]
		if requestIsNotification(raw) {
			if body.Effort != "high" {
				t.Error("result handler lost main effort", body.Effort)
			}
			if last.Role == "tool" {
				batchReply(w, "")
				return
			}
			start := strings.LastIndex(last.Content, "\n[")
			if start < 0 {
				t.Error("missing result payload")
				return
			}
			var results []struct {
				TaskID string `json:"task_id"`
				Retry  bool   `json:"retry_allowed"`
			}
			if err := json.Unmarshal([]byte(last.Content[start+1:]), &results); err != nil || len(results) != 1 {
				t.Error("bad result payload", err)
				return
			}
			if results[0].Retry {
				lifecycleToolReply(w, "retry_task", map[string]string{"task_id": results[0].TaskID, "instructions": "Retry the temporary read failure"})
				return
			}
			batchReply(w, "Recovered experiment")
			return
		}
		if strings.Contains(body.Messages[0].Content, "You cannot delegate.") {
			if body.Effort != "low" {
				t.Error("replacement lost task effort", body.Effort)
			}
			if workers.Add(1) == 1 {
				http.Error(w, "temporary read failure", http.StatusBadRequest)
				return
			}
			batchReply(w, "done")
			return
		}
		if body.Effort != "high" {
			t.Error("main lost effort", body.Effort)
		}
		if main.Add(1) == 1 {
			lifecycleToolReply(w, "create_task", map[string]string{"text": "Experiment task\nReturn done."})
			return
		}
		batchReply(w, "")
	}, 3)
	_, err := s.Ask(ctx, "main", AskRequest{Message: protocol.SendMessage{RequestID: "retry-experiment", ConversationID: "main", Text: "Delegate"}, Effort: "high", TaskEffort: "low"})
	if err != nil {
		t.Fatal(err)
	}
	var status protocol.AgentExecutionStatus
	for {
		status, err = s.AgentExecutionStatus(ctx, "main", "retry-experiment")
		if err != nil {
			t.Fatal(err)
		}
		if status.State != "running" {
			break
		}
		select {
		case <-time.After(10 * time.Millisecond):
		case <-ctx.Done():
			t.Fatalf("%+v %v", status, ctx.Err())
		}
	}
	if status.State != "completed" || len(status.Tasks) != 2 || workers.Load() != 2 || len(status.ReplyIDs) == 0 || status.ReplyIDs[len(status.ReplyIDs)-1] == "" {
		t.Fatalf("%+v workers=%d", status, workers.Load())
	}
}
