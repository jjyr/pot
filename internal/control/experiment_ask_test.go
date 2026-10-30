package control

import (
	"context"
	"encoding/json"
	"github.com/jjyr/pot/internal/usage"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAskWaitsForWorkerAndResultProcessing(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	workerRelease := make(chan struct{})
	summaryRelease := make(chan struct{})
	summaryEntered := make(chan struct{})
	var mainCalls atomic.Int32
	s, server := batchService(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body struct {
			Messages []struct{ Content string }
			Model    string
			Effort   string `json:"reasoning_effort"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Error(err)
			return
		}
		if requestIsNotification(raw) {
			if body.Model != "second" || body.Effort != "high" {
				t.Errorf("result config: %s", raw)
			}
			close(summaryEntered)
			select {
			case <-summaryRelease:
			case <-r.Context().Done():
				return
			}
			batchReply(w, "Final task result")
			return
		}
		if strings.Contains(body.Messages[0].Content, "You cannot delegate.") {
			if body.Model != "first" || body.Effort != "low" {
				t.Errorf("worker config: %s", raw)
			}
			select {
			case <-workerRelease:
			case <-r.Context().Done():
				return
			}
			batchReply(w, "Worker finished")
			return
		}
		round := mainCalls.Add(1)
		if round <= 2 {
			if body.Model != "second" || body.Effort != "high" {
				t.Errorf("main config: %s", raw)
			}
		} else {
			if body.Model != "first" || body.Effort != "" {
				t.Errorf("override leaked: %s", raw)
			}
		}
		if round == 1 {
			lifecycleToolReply(w, "create_task", map[string]string{"text": "Experiment task\nReturn the word done.", "model": "second"})
			return
		}
		batchReply(w, "Initial acknowledgement")
	}, 3)
	s.Profiles.directory = fixtureDirectory(t, 128000)
	catalog := "fixture"
	profile, e := s.Profiles.Save(ctx, SaveProfile{RequestID: "experiment-provider", ProfileID: "experiment-provider", Name: "Experiment", Model: "first", Endpoint: server.URL, Protocol: "openai_chat", APIKey: "test-key", MaxOutputTokens: 100, ModelSettings: &core.ModelSettings{CatalogProvider: &catalog}})
	if e != nil {
		t.Fatal(e)
	}
	spec, e := s.Profiles.Resolve(profile.ID)
	if e != nil {
		t.Fatal(e)
	}
	c, err := s.Conversation(ctx, "main")
	if err != nil {
		t.Fatal(err)
	}
	thread, e := c.Thread(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if err = c.Configure(ctx, spec, thread.Generation); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Ask(ctx, "main", AskRequest{Message: protocol.SendMessage{RequestID: "experiment", ConversationID: "main", Text: "Delegate the experiment"}, Model: "second", Effort: "high", TaskModel: "first", TaskEffort: "low"}); err != nil {
		t.Fatal(err)
	}
	streamCtx, stopStream := context.WithCancel(ctx)
	updates := make(chan protocol.AgentExecutionStatus, 32)
	streamDone := make(chan error, 1)
	go func() {
		streamDone <- s.StreamAgentExecution(streamCtx, "main", "experiment", func(value protocol.AgentExecutionStatus) error {
			select {
			case updates <- value:
				return nil
			case <-streamCtx.Done():
				return streamCtx.Err()
			}
		})
	}()
	defer func() { stopStream(); <-streamDone }()
	awaitConversationContext(t, ctx, c, func(s protocol.Snapshot) bool { return s.Turn == nil && mainCalls.Load() >= 2 })
	status, err := s.AgentExecutionStatus(ctx, "main", "experiment")
	if err != nil || status.State != "running" || len(status.Tasks) != 1 {
		t.Fatalf("premature completion: %+v %v", status, err)
	}
	close(workerRelease)
	select {
	case <-summaryEntered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	status, err = s.AgentExecutionStatus(ctx, "main", "experiment")
	if err != nil || status.State != "running" {
		t.Fatalf("result processing not awaited: %+v %v", status, err)
	}
	close(summaryRelease)
	for {
		select {
		case status = <-updates:
			if status.Terminal {
				goto completed
			}
		case <-ctx.Done():
			t.Fatalf("missing terminal push: %+v %v", status, ctx.Err())
		}
	}
completed:

	if status.State != "completed" || len(status.Turns) != 2 || len(status.ReplyIDs) == 0 || status.ReplyIDs[len(status.ReplyIDs)-1] == "" {
		t.Fatalf("%+v", status)
	}

	q := usage.Query{Days: 14, Timezone: "UTC"}
	global, err := s.ExperimentUsage(ctx, "", "", q, true, time.Now().UnixMilli())
	if err != nil || global.Scope != "" || len(global.Models) != 3 || global.Communications == nil || global.Communications.Count < 1 {
		t.Fatalf("global usage: %+v %v", global, err)
	}
	task, err := s.ExperimentUsage(ctx, "", status.Tasks[0].ID, q, false, time.Now().UnixMilli())
	if err != nil || len(task.Models) != 1 || task.Models[0].Agent != "task" {
		t.Fatalf("task usage: %+v %v", task, err)
	}
	q.Turn = "experiment"
	turn, err := s.ExperimentUsage(ctx, "main", "", q, true, time.Now().UnixMilli())
	if err != nil || len(turn.Models) != 1 || turn.Communications != nil || turn.Tools.Calls != 1 {
		t.Fatalf("turn usage: %+v %v", turn, err)
	}
	if _, err = s.ExperimentUsage(ctx, "main", status.Tasks[0].ID, q, false, time.Now().UnixMilli()); err == nil {
		t.Fatal("ambiguous scope accepted")
	}

	after, err := c.Configuration(ctx)
	if err != nil || after.Config.SpecHash != spec.SpecHash {
		t.Fatalf("saved config changed: %+v %v", after, err)
	}
	if _, err = c.Send(ctx, protocol.SendMessage{RequestID: "ordinary", ConversationID: "main", Text: "ordinary request"}); err != nil {
		t.Fatal(err)
	}
	awaitConversationContext(t, ctx, c, func(s protocol.Snapshot) bool { return s.Turn == nil && mainCalls.Load() >= 3 })

}
