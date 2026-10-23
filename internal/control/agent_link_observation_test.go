package control

import (
	"context"
	"encoding/json"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/execution"
	"github.com/jjyr/pot/pkg/protocol"
	"strconv"
	"strings"
	"testing"
)

func TestLinkObservationCountsCompleteTaskSetAndScopesOutbox(t *testing.T) {
	t.Parallel()
	s := agentControl(t, "observation-node").service
	ctx := context.Background()
	s.cancel()
	s.taskLife.wg.Wait()
	s.pluginWG.Wait()
	s.mu.Lock()
	conversationsBefore := len(s.conversations)
	s.mu.Unlock()
	// Only metadata is queried; no execution or model request is started.
	for index, phase := range []string{"running", "queued", "queued", "waiting", "waiting_approval", "complete", "failed"} {
		id := "fixture-" + strconv.Itoa(index)
		body, _ := json.Marshal(map[string]any{"node_id": "observation-node", "request": protocol.CreateTask{}, "info": protocol.TaskInfo{ID: id, AssistantID: "main", Phase: protocol.TaskPhase(phase)}})
		if _, err := s.Tasks.DB().Exec("INSERT INTO tasks(assistant_id,id,phase,created,body)VALUES('main',?,?,1,?)", id, phase, string(body)); err != nil {
			t.Fatal(err)
		}
	}

	// Caller-side receipts and historical destinations must not inflate this
	// node's execution counts. An unavailable local run is not an allowance wait.
	for _, phase := range []string{"running", "queued", "waiting", "waiting_approval"} {
		body, _ := json.Marshal(map[string]any{"execution": map[string]any{}, "info": protocol.TaskInfo{ID: "linked-" + phase, AssistantID: "main", Phase: protocol.TaskPhase(phase)}})
		if _, err := s.Tasks.DB().Exec("INSERT INTO tasks(assistant_id,id,phase,created,body)VALUES('main',?,?,1,?)", "linked-"+phase, phase, string(body)); err != nil {
			t.Fatal(err)
		}
	}
	for _, kind := range []string{"target", "launch", "unavailable"} {
		info := protocol.TaskInfo{ID: kind, AssistantID: "main", Phase: protocol.TaskRunning}
		body := map[string]any{"node_id": "observation-node", "request": protocol.CreateTask{}, "info": info}
		if kind == "target" {
			target := core.RuntimeTarget("retired-node")
			info.ExecutionTarget = &target
		}
		if kind == "launch" {
			body["launch"] = map[string]any{"control_id": "observation-node", "runtime_id": "retired-node", "execution_target": core.RuntimeTarget("retired-node")}
		}
		if kind == "unavailable" {
			info.Phase = protocol.TaskWaiting
			info.RemoteWaiting = true
		}
		body["info"] = info
		raw, _ := json.Marshal(body)
		if _, err := s.Tasks.DB().Exec("INSERT INTO tasks(assistant_id,id,phase,created,body)VALUES('main',?,?,1,?)", kind, info.Phase, string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	for _, peer := range []string{"peer-one", "peer-two"} {
		if _, err := s.Mailbox.Send(ctx, "message-"+peer, protocol.AgentRef{NodeID: "observation-node", Kind: "assistant", AgentID: "main"}, protocol.SendMail{To: protocol.AgentRef{NodeID: peer, Kind: "assistant", AgentID: "main"}, Text: "private message"}, nil); err != nil {
			t.Fatal(err)
		}
	}
	value, err := (potLinkBackend{s}).Observe(ctx, "peer-one")
	if err != nil {
		t.Fatal(err)
	}
	if len(value.Assistants) != 1 {
		t.Fatal(value)
	}
	a := value.Assistants[0]
	if a.Running != 1 || a.Queued != 2 || a.WaitingAllowance != 1 || a.WaitingApproval != 1 || value.Outbox.Pending != 1 || value.Outbox.OldestAtMS <= 0 {
		t.Fatalf("bad observation: %+v", value)
	}
	raw, _ := json.Marshal(value)
	if strings.Contains(string(raw), "private message") {
		t.Fatal("private payload leaked")
	}
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		s.options.Manager.Execute(ctx, execution.Identity{Owner: "main", RequestID: "peer-correspondence", Generation: 1, Role: "assistant"}, func(context.Context) execution.Result {
			close(entered)
			<-release
			return execution.Result{Phase: execution.Succeeded}
		})
	}()
	<-entered
	active, observeErr := (potLinkBackend{s}).Observe(ctx, "peer-one")
	close(release)
	<-done
	if observeErr != nil || active.Assistants[0].ChatState != "running" {
		t.Fatal("peer correspondence was not visible", active, observeErr)
	}
	unknown := execution.Identity{Owner: "fixture-0", RequestID: "unknown-cleanup", Generation: 1, Role: "task"}
	s.options.Manager.Execute(ctx, unknown, func(context.Context) execution.Result {
		return execution.Result{Phase: execution.Interrupted, Err: execution.ErrUnconfirmed}
	})
	fenced, observeErr := (potLinkBackend{s}).Observe(ctx, "peer-one")
	if err := s.options.Manager.ReconcileInterrupted(unknown); err != nil {
		t.Fatal(err)
	}
	if observeErr != nil || !fenced.Assistants[0].Fenced {
		t.Fatal("uncertain task cleanup was not visible", fenced, observeErr)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.conversations) != conversationsBefore {
		t.Fatal("reading observation opened a conversation")
	}
}
