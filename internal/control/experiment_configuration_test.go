package control

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

func TestExperimentQueueIsolationAndDurableConfiguration(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	var mu sync.Mutex
	var efforts []string
	s, _ := batchService(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var input struct {
			Effort string `json:"reasoning_effort"`
		}
		if err := json.Unmarshal(raw, &input); err != nil {
			t.Error(err)
			return
		}
		mu.Lock()
		efforts = append(efforts, input.Effort)
		n := len(efforts)
		mu.Unlock()
		if n == 1 {
			close(entered)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		batchReply(w, "done")
	}, 1)
	c, err := s.Conversation(ctx, "main")
	if err != nil {
		t.Fatal(err)
	}
	before, err := c.Configuration(ctx)
	if err != nil {
		t.Fatal(err)
	}
	first := AskRequest{Message: protocol.SendMessage{RequestID: "first-experiment", ConversationID: "main", Text: "first"}, Effort: "high"}
	accepted, err := s.Ask(ctx, "main", first)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	again, err := s.Ask(ctx, "main", first)
	if err != nil || again.MessageID != accepted.MessageID {
		t.Fatal(again, err)
	}
	changed := first
	changed.Effort = "low"
	if _, err = s.Ask(ctx, "main", changed); err == nil {
		t.Fatal("changed override reused request identity")
	}
	second := AskRequest{Message: protocol.SendMessage{RequestID: "second-experiment", ConversationID: "main", Text: "second"}, Effort: "low"}
	if _, err = s.Ask(ctx, "main", second); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Send(ctx, protocol.SendMessage{RequestID: "ordinary", ConversationID: "main", Text: "third"}); err != nil {
		t.Fatal(err)
	}
	invalid := AskRequest{Message: protocol.SendMessage{RequestID: "invalid", ConversationID: "main", Text: "invalid"}, Effort: "nonsense"}
	if _, err = s.Ask(ctx, "main", invalid); err == nil {
		t.Fatal("invalid effort accepted")
	}
	invalid.Effort = "high"
	invalid.TaskProvider = "missing"
	if _, err = s.Ask(ctx, "main", invalid); err == nil {
		t.Fatal("missing task provider accepted")
	}
	close(release)
	awaitConversationContext(t, ctx, c, func(s protocol.Snapshot) bool {
		mu.Lock()
		defer mu.Unlock()
		return s.Turn == nil && len(efforts) == 3
	})
	mu.Lock()
	got := append([]string(nil), efforts...)
	mu.Unlock()
	if len(got) != 3 || got[0] != "high" || got[1] != "low" || got[2] != "" {
		t.Fatal("configuration batch leak", got)
	}
	after, err := c.Configuration(ctx)
	if err != nil || after.Config.SpecHash != before.Config.SpecHash || after.Revision != before.Revision {
		t.Fatal("saved configuration changed", err)
	}
	snap, err := c.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range snap.Messages {
		if m.RequestID != nil && *m.RequestID == "invalid" {
			t.Fatal("invalid request entered conversation")
		}
	}
	// A fresh database handle has the same frozen model selection; no process map
	// or temporary global assistant setting is needed to recover it.
	db, err := storage.OpenDatabase(filepath.Join(s.options.Root, "tasks.sqlite"), storage.TasksDatabase)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	restored := &Service{tasksDB: db}
	spec, err := restored.experimentTurnConfig(ctx, "main", "first-experiment", *before.Config)
	if err != nil || spec.Params.ReasoningEffort == nil || *spec.Params.ReasoningEffort != "high" {
		t.Fatal(spec, err)
	}
	ordinary, err := restored.experimentTurnConfig(ctx, "main", "ordinary", *before.Config)
	if err != nil || ordinary.SpecHash != before.Config.SpecHash {
		t.Fatal(ordinary, err)
	}
}
