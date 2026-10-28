package control

import (
	"context"
	"errors"
	"github.com/jjyr/pot/internal/tasks"
	"github.com/jjyr/pot/pkg/agentlink"
	"testing"

	"github.com/jjyr/pot/pkg/protocol"
)

func TestLinkedReceiptsKeepCancellationActiveAndFastContinuationVisible(t *testing.T) {
	t.Parallel()
	backend, _ := linkBackendFixture(t)
	ctx := context.Background()
	record := tasks.Record{AssistantID: "main", Request: protocol.CreateTask{RequestID: "receipt-request", Text: "Remote work"}, Info: protocol.TaskInfo{ID: "receipt-task", AssistantID: "main", Phase: protocol.TaskQueued, AgentLink: &protocol.AgentLinkOrigin{LinkID: "peer-link", NodeID: "peer"}}, Execution: &tasks.Execution{Request: agentlink.TaskRequest{From: protocol.AgentRef{NodeID: "node", Kind: "assistant", AgentID: "main"}}}}
	if err := backend.s.Tasks.AcceptExecution(ctx, record); err != nil {
		t.Fatal(err)
	}
	storedRecord, err := backend.s.Tasks.Get(ctx, "main", record.Info.ID)
	if err != nil {
		t.Fatal(err)
	}
	record = *storedRecord
	// A continuation can finish before its first observation. Its completion must
	// use the same catalogue phase and result path as a polled completion.
	projectTaskReceipt(&record, agentlink.Task{ID: "remote-task", RunID: "first-run", State: "completed", Result: "first result"})
	if ok, err := backend.s.Tasks.UpdateExecution(ctx, record, ""); err != nil || !ok {
		t.Fatal(ok, err)
	}
	page, err := backend.s.Tasks.Page(ctx, "main", protocol.TaskQuery{Scope: "attention"})
	if err != nil || page.Total != 1 || page.Items[0].Phase != protocol.TaskComplete {
		t.Fatal(page, err)
	}
	// A cancellation acknowledgement still describes unfinished execution. It
	// cannot disappear from active work or emit a terminal result.
	projectTaskReceipt(&record, agentlink.Task{ID: "remote-task", RunID: "second-run", State: "cancelling"})
	if ok, err := backend.s.Tasks.UpdateExecution(ctx, record, "first-run"); err != nil || !ok {
		t.Fatal(ok, err)
	}
	stored, err := backend.s.Tasks.Get(ctx, "main", record.Info.ID)
	if err != nil || stored.Result != nil || stored.Outcome != nil {
		t.Fatal(stored, err)
	}
	page, err = backend.s.Tasks.Page(ctx, "main", protocol.TaskQuery{Scope: "active"})
	if err != nil || page.Total != 1 || page.Counts.Attention != 0 {
		t.Fatal(page, err)
	}
	var outcomes int
	if err := backend.s.Tasks.DB().QueryRow("SELECT COUNT(*) FROM task_outcomes").Scan(&outcomes); err != nil || outcomes != 1 {
		t.Fatal("cancellation invented completion", outcomes, err)
	}
}

func TestLinkedGuidanceCannotUpgradeReferenceOrForeignSender(t *testing.T) {
	t.Parallel()
	backend, _ := linkBackendFixture(t)
	ctx := context.Background()
	from := protocol.AgentRef{NodeID: "node", Kind: "assistant", AgentID: "main"}
	record := tasks.Record{AssistantID: "main", Request: protocol.CreateTask{RequestID: "guidance-source", Text: "Original goal"}, Info: protocol.TaskInfo{ID: "guidance-task", AssistantID: "main", Phase: protocol.TaskComplete, AgentLink: &protocol.AgentLinkOrigin{LinkID: "unavailable-link", NodeID: "peer"}}, Execution: &tasks.Execution{Request: agentlink.TaskRequest{From: from}, Receipt: agentlink.Task{ID: "original-task", RunID: "original-run", State: "completed"}}}
	if err := backend.s.Tasks.AcceptExecution(ctx, record); err != nil {
		t.Fatal(err)
	}
	parent := protocol.Envelope{ID: "peer-input", From: protocol.AgentRef{NodeID: "peer", Kind: "assistant", AgentID: "other"}, To: from, Text: "Reference only"}
	contexts := []context.Context{
		context.WithValue(context.WithValue(ctx, taskInputSenderKey{}, from), mailParentKey{}, parent),
		context.WithValue(ctx, taskInputSenderKey{}, parent.From),
		context.WithValue(ctx, taskInputSenderKey{}, protocol.AgentRef{NodeID: "node", Kind: "task", AgentID: "sibling"}),
	}
	for _, inputCtx := range contexts {
		if _, err := backend.s.guideTaskExecution(inputCtx, "main", record.Info.ID, protocol.TaskInput{RequestID: "continuation", Text: "Continue with these instructions"}); !errors.Is(err, agentlink.ErrUnauthorized) {
			t.Fatal("reference or foreign sender reached owner guidance", err)
		}
	}
	stored, err := backend.s.Tasks.Get(ctx, "main", record.Info.ID)
	if err != nil || stored.Info.Phase != protocol.TaskComplete || stored.Execution.Receipt.RunID != "original-run" || stored.Info.Pending != 0 {
		t.Fatal("unauthorized input changed execution", stored, err)
	}
}
