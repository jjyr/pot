package control

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/schedule"
	"github.com/jjyr/pot/internal/tasks"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

func TestScheduledAndManualOutcomesShareSummaryDelivery(t *testing.T) {
	for _, failSummary := range []bool{false, true} {
		t.Run(fmt.Sprintf("summary-fails=%v", failSummary), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			foregroundEntered, foregroundRelease := make(chan struct{}), make(chan struct{})
			workerRelease := make(chan struct{})
			summaryEntered, summaryRelease := make(chan struct{}), make(chan struct{})
			var summaries atomic.Int32
			s, _ := batchService(t, func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				var body struct {
					Messages []struct {
						Content string `json:"content"`
					} `json:"messages"`
					Tools []json.RawMessage `json:"tools"`
				}
				if err := json.Unmarshal(raw, &body); err != nil {
					t.Error(err)
					return
				}
				last := body.Messages[len(body.Messages)-1].Content
				if strings.Contains(body.Messages[0].Content, "Process background updates") {
					if summaries.Add(1) != 1 {
						t.Error("summary replayed")
						batchReply(w, "duplicate")
						return
					}
					if !strings.Contains(last, "Manual task") || !strings.Contains(last, "Scheduled monitor") || !strings.Contains(last, "observed-scheduled-fact") || !strings.Contains(last, "report.txt") || !strings.Contains(last, "https://example.com/evidence") {
						t.Error("summary lost mixed outcomes, evidence or artifacts", string(raw))
					}
					close(summaryEntered)
					select {
					case <-summaryRelease:
					case <-r.Context().Done():
						return
					}
					if failSummary {
						http.Error(w, "summary failed", http.StatusBadRequest)
						return
					}
					batchReply(w, "Manual task completed; scheduled observation changed")
					return
				}
				if strings.Contains(last, "foreground gate") {
					close(foregroundEntered)
					select {
					case <-foregroundRelease:
					case <-r.Context().Done():
						return
					}
					batchReply(w, "foreground done")
					return
				}
				select {
				case <-workerRelease:
				case <-r.Context().Done():
					return
				}
				batchReply(w, "observed-scheduled-fact https://example.com/evidence")
			}, 4)
			c, err := s.Conversation(ctx, "main")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := c.Send(ctx, protocol.SendMessage{RequestID: "hold-summary", ConversationID: "main", Text: "foreground gate"}); err != nil {
				t.Fatal(err)
			}
			select {
			case <-foregroundEntered:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			spec := protocol.ScheduleSpec{Title: "Scheduled monitor", Goal: "Check the scheduled observation", Timezone: "Asia/Shanghai", Kind: "interval", EveryMinutes: 60, Misfire: "coalesce", Notify: "all", Enabled: false}
			raw, err := s.Schedules.Manage(ctx, "main", schedule.Request{RequestID: "create-monitor", Action: "create", Spec: &spec}, storage.Now())
			if err != nil {
				t.Fatal(err)
			}
			var info protocol.ScheduleInfo
			if err := json.Unmarshal(raw, &info); err != nil {
				t.Fatal(err)
			}
			raw, err = s.Schedules.Manage(ctx, "main", schedule.Request{RequestID: "run-monitor", Action: "run", ScheduleID: info.ID}, storage.Now())
			if err != nil {
				t.Fatal(err)
			}
			var accepted struct {
				TaskID string `json:"task_id"`
			}
			if err := json.Unmarshal(raw, &accepted); err != nil || accepted.TaskID == "" {
				t.Fatal(string(raw), err)
			}
			manual, err := s.Tasks.AcceptLocal(ctx, "main", protocol.CreateTask{RequestID: "manual-task", Text: "Manual task\nFinish the manual work"})
			if err != nil {
				t.Fatal(err)
			}
			var root string
			for {
				root, err = s.taskWorkspacePath(ctx, accepted.TaskID)
				if err == nil {
					break
				}
				select {
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-time.After(5 * time.Millisecond):
				}
			}
			if err := os.MkdirAll(filepath.Join(root, ".pot-artifacts"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, ".pot-artifacts", "report.txt"), []byte("Detailed scheduled findings"), 0600); err != nil {
				t.Fatal(err)
			}
			// An old or task-specific JSON report has no scheduler semantics.
			if err := os.WriteFile(filepath.Join(root, ".pot-schedule-result.json"), []byte(`{"tool_failures_observed":3,"status":"success_with_observability_limits"}`), 0600); err != nil {
				t.Fatal(err)
			}
			if failSummary {
				if err := os.Symlink(t.TempDir(), filepath.Join(root, ".pot-artifacts", "escape")); err != nil {
					t.Fatal(err)
				}
			}
			close(workerRelease)
			sub := s.Tasks.Subscribe()
			defer sub.Close()
			ids := []string{accepted.TaskID, manual.Info.ID}
			for {
				ended := true
				for _, id := range ids {
					task, err := s.Tasks.Get(ctx, "main", id)
					if err != nil {
						t.Fatal(err)
					}
					ended = ended && task.Info.Phase == protocol.TaskComplete
				}
				if ended {
					stored, err := s.Tasks.Get(ctx, "main", accepted.TaskID)
					if err != nil || stored.Outcome == nil || stored.Outcome.Error != "" || (stored.Outcome.ArtifactsError != "") != failSummary {
						t.Fatal("attachment error changed execution outcome", stored, err)
					}
					break
				}
				if _, err := sub.Next(ctx); err != nil {
					t.Fatal(err)
				}
			}
			if summaries.Load() != 0 {
				t.Fatal("summary overtook foreground user")
			}
			close(foregroundRelease)
			select {
			case <-summaryEntered:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			for _, id := range ids {
				var state string
				if err := s.Tasks.DB().QueryRowContext(ctx, `SELECT state FROM agent_messages WHERE json_extract(envelope,'$.result.task_id')=?`, id).Scan(&state); err != nil || state != "processing" {
					t.Fatal("outcome acknowledged before reply", id, state, err)
				}
			}
			close(summaryRelease)
			changes, stop, err := c.Subscribe(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer stop()
			for {
				select {
				case snapshot := <-changes:
					if snapshot.Turn != nil {
						continue
					}
					for _, id := range ids {
						want := "processed"
						if failSummary {
							want = "failed"
						}
						var state string
						if err := s.Tasks.DB().QueryRowContext(ctx, `SELECT state FROM agent_messages WHERE json_extract(envelope,'$.result.task_id')=?`, id).Scan(&state); err != nil || state != want {
							t.Fatal(id, state, want, err)
						}
					}
					if summaries.Load() != 1 {
						t.Fatal(summaries.Load())
					}
					return
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
		})
	}
}

func TestGetTaskReturnsFinalizedOutcome(t *testing.T) {
	ctx := context.Background()
	s, _ := batchService(t, func(w http.ResponseWriter, r *http.Request) { http.Error(w, "execution failed", http.StatusBadRequest) }, 1)
	record, err := s.Tasks.AcceptLocal(ctx, "main", protocol.CreateTask{RequestID: "query", Text: "Inspect outcome\nDo the work"})
	if err != nil {
		t.Fatal(err)
	}
	// Stop before or during admission; either path produces the canonical result.
	if err := s.Tasks.Stop(ctx, "main", record.Info.ID, "interrupt"); err != nil {
		t.Fatal(err)
	}
	sub := s.Tasks.Subscribe()
	defer sub.Close()
	deadline, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for {
		stored, err := s.Tasks.Get(deadline, "main", record.Info.ID)
		if err != nil {
			t.Fatal(err)
		}
		if stored.Outcome != nil {
			break
		}
		if _, err := sub.Next(deadline); err != nil {
			t.Fatal(err)
		}
	}
	args, _ := json.Marshal(map[string]string{"task_id": record.Info.ID})
	value, err := s.productCall(deadline, "main", tasks.ID(), "get_task", args)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(value.(map[string]any)["result"])
	var result protocol.TaskResultEvent
	if err = json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if result.Origin.Kind != "manual" || result.TaskID != record.Info.ID {
		t.Fatal(value)
	}
}
