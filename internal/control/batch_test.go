package control

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/inbox"
	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/pkg/execution"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

func batchService(t *testing.T, handler http.HandlerFunc, capacity int) (*Service, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	manager, err := execution.New("local", "boot", execution.Limits{MaxActive: capacity, MaxQueued: 16})
	if err != nil {
		t.Fatal(err)
	}
	keys, _ := secrets.NewMemoryKey(make([]byte, 32))
	s, err := Open(Options{Root: t.TempDir(), RuntimeID: "local", BootID: "boot", Manager: manager, Keys: keys})
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close(); server.Close(); manager.Stop() })
	ctx := context.Background()
	if _, err = s.Registry.Create(ctx, "main", "Main"); err != nil {
		t.Fatal(err)
	}
	profile, err := s.Profiles.Save(ctx, SaveProfile{RequestID: "profile", ProfileID: "test", Name: "Test", Model: "model", Endpoint: server.URL, Protocol: "openai_chat", APIKey: "test-key", MaxOutputTokens: 100})
	if err != nil {
		t.Fatal(err)
	}
	spec, err := s.Profiles.Resolve(profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.Conversation(ctx, "main")
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Configure(ctx, spec, 0); err != nil {
		t.Fatal(err)
	}
	return s, server
}

func batchReply(w http.ResponseWriter, text string) {
	event, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": text}, "finish_reason": "stop"}}, "usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 5}})
	w.Header().Set("Content-Type", "text/event-stream")
	w.Write([]byte("data: " + string(event) + "\n\ndata: [DONE]\n\n"))
}

func TestQueuedUserAndTaskOutcomesUseSeparateAggregatedModelTurns(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	var users, summaries atomic.Int32
	s, _ := batchService(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body := string(raw)
		if strings.Contains(body, "Summarize background task results") {
			summaries.Add(1)
			if !strings.Contains(body, "Task A") || !strings.Contains(body, "Task B") || !strings.Contains(body, "HTTP 400") {
				t.Error("summary omitted a success or failure", body)
			}
			batchReply(w, "A succeeded; B failed")
			return
		}
		if strings.Contains(body, "worker goal B") {
			http.Error(w, "worker-failed-before-output", http.StatusBadRequest)
			return
		}
		if strings.Contains(body, "worker goal A") {
			batchReply(w, "A completed")
			return
		}
		if users.Add(1) == 1 {
			close(entered)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		} else {
			var parsed struct {
				Messages []struct {
					Content string `json:"content"`
				} `json:"messages"`
			}
			json.Unmarshal(raw, &parsed)
			last := parsed.Messages[len(parsed.Messages)-1].Content
			if !strings.Contains(last, "user-one") || !strings.Contains(last, "user-two") || strings.Contains(last, "artifact_task_id") {
				t.Error("user batch mixed with outcomes or lost input", last)
			}
		}
		batchReply(w, "user handled")
	}, 4)
	c, _ := s.Conversation(ctx, "main")
	if _, err := c.Send(ctx, protocol.SendMessage{RequestID: "hold", ConversationID: "main", Text: "foreground gate"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	var taskIDs []string
	for i, goal := range []string{"Task A\nworker goal A", "Task B\nworker goal B"} {
		record, err := s.Tasks.Create(ctx, "main", protocol.CreateTask{RequestID: fmt.Sprintf("task-%d", i), Text: goal})
		if err != nil {
			t.Fatal(err)
		}
		taskIDs = append(taskIDs, record.Info.ID)
	}
	sub := s.Tasks.Subscribe()
	defer sub.Close()
	for {
		ended := true
		for _, id := range taskIDs {
			record, err := s.Tasks.Get(ctx, "main", id)
			if err != nil {
				t.Fatal(err)
			}
			ended = ended && record.Info.Phase.Scope() != "active"
		}
		if ended {
			break
		}
		if _, err := sub.Next(ctx); err != nil {
			t.Fatal(err)
		}
	}
	for _, text := range []string{"user-one", "user-two"} {
		if _, err := c.Send(ctx, protocol.SendMessage{RequestID: text, ConversationID: "main", Text: text}); err != nil {
			t.Fatal(err)
		}
	}
	if summaries.Load() != 0 {
		t.Fatal("results overtook foreground user")
	}
	close(release)
	changes, stop, err := c.Subscribe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	for {
		select {
		case snapshot := <-changes:
			if snapshot.Turn == nil && summaries.Load() == 1 {
				notices, err := s.Tasks.Notifications(ctx, "main")
				if err != nil {
					t.Fatal(err)
				}
				if len(notices) == 0 {
					if users.Load() != 2 {
						t.Fatal("queued users used separate turns", users.Load())
					}
					return
				}
			}
		case <-ctx.Done():
			t.Fatal("batches did not finish", ctx.Err(), users.Load(), summaries.Load())
		}
	}
}

func TestAgentCommunicationBatchesBySenderNotWireConversation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	var calls atomic.Int32
	s, _ := batchService(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body := string(raw)
		calls.Add(1)
		if strings.Contains(body, "alpha-first") {
			if !strings.Contains(body, "alpha-second") || strings.Contains(body, "beta-only") {
				t.Error("peer group split or mixed", body)
			}
		} else if !strings.Contains(body, "beta-only") || strings.Contains(body, "alpha-second") {
			t.Error("peer context mixed", body)
		}
		batchReply(w, "private peer result")
	}, 2)
	// Atomic enqueue ensures the receiver observes all messages at one boundary.
	c, _ := s.Conversation(ctx, "main")
	err := storage.Transaction(ctx, s.Tasks.DB(), func(tx *sql.Tx) error {
		for i, text := range []string{"alpha-first", "beta-only", "alpha-second"} {
			peer := "alpha"
			if i == 1 {
				peer = "beta"
			}
			e := protocol.Envelope{Kind: "message", ID: fmt.Sprintf("peer-%d", i), From: protocol.AgentRef{ControlID: "local", Kind: "assistant", AgentID: peer}, To: protocol.AgentRef{ControlID: "local", Kind: "assistant", AgentID: "main"}, ConversationID: fmt.Sprintf("00000000-0000-4000-8000-%012d", i+1), Text: text}
			if err := inbox.EnqueueTx(ctx, tx, e, "in", "queued"); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	s.wakeTasks()
	sub := s.Mailbox.Changes.Subscribe()
	defer sub.Close()
	for {
		history, err := s.Mailbox.History(ctx, "main", 0)
		if err != nil {
			t.Fatal(err)
		}
		processed := 0
		for _, item := range history {
			if item.State == "processed" {
				processed++
			}
			if item.State == "failed" || item.State == "interrupted" {
				t.Fatal(item)
			}
		}
		if processed == 3 {
			if calls.Load() != 2 {
				t.Fatal("agent grouping did not reduce turns", calls.Load())
			}
			snapshot, _ := c.Snapshot(ctx)
			if len(snapshot.Messages) != 0 {
				t.Fatal("peer batch entered user chat")
			}
			return
		}
		if _, err := sub.Next(ctx); err != nil {
			t.Fatal(err, calls.Load())
		}
	}
}
