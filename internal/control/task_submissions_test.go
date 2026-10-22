package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jjyr/pot/internal/tasks"
	"github.com/jjyr/pot/pkg/agentlink"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jjyr/pot/pkg/protocol"
)

func TestAgentToolsShareTaskPortAndCatalogueAcrossLocalAndPeer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { batchReply(w, "task result") }))
	defer model.Close()
	source := taskControl(t, "source", model.URL)
	receiver := taskControl(t, "receiver", model.URL)
	a := &agentControlFixture{service: source, endpoint: controlPeerEndpoint(source)}
	b := &agentControlFixture{service: receiver, endpoint: controlPeerEndpoint(receiver)}
	link := connectAgentControls(t, a, b)
	requests := []protocol.CreateTask{{Text: "Local task\nReturn a result."}, {Text: "Peer task\nReturn a result.", LinkID: link.ID}}
	ids := []string{}
	for i, request := range requests {
		body, _ := json.Marshal(request)
		accepted, err := source.productCall(ctx, "main", []string{"local-request", "peer-request"}[i], "create_task", body)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, accepted.(protocol.TaskInfo).ID)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		source.syncTaskExecutions(ctx)
		complete := 0
		for _, id := range ids {
			record, err := source.Tasks.Get(ctx, "main", id)
			if err != nil || record == nil {
				t.Fatal(record, err)
			}
			if record.Info.Phase == protocol.TaskComplete {
				complete++
			}
		}
		if complete == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("tasks did not complete")
		}
		time.Sleep(10 * time.Millisecond)
	}
	page, err := source.productCall(ctx, "main", "list", "list_tasks", json.RawMessage(`{"scope":"all"}`))
	if err != nil || page.(protocol.TaskPage).Total != 2 {
		t.Fatal(page, err)
	}
	for _, id := range ids {
		body, _ := json.Marshal(map[string]string{"task_id": id})
		summary, err := source.productCall(ctx, "main", "read", "get_task", body)
		if err != nil {
			t.Fatal(err)
		}
		result := summary.(map[string]any)["result"].(map[string]any)
		if result["text"] != "task result" {
			t.Fatal(summary)
		}
	}
	local, _ := source.Tasks.Get(ctx, "main", ids[0])
	peer, _ := source.Tasks.Get(ctx, "main", ids[1])
	if local.Config == nil || local.Execution != nil || peer.Config != nil || peer.Launch != nil || peer.Execution.Receipt.ID == "" {
		t.Fatal("adapter state leaked into generic task", local, peer)
	}
}

func TestAcceptedTaskCancellationPersistsThroughDisconnectAndRestart(t *testing.T) {
	ctx := context.Background()
	backend, _ := linkBackendFixture(t)
	source := &agentControlFixture{service: backend.s, endpoint: controlPeerEndpoint(backend.s)}
	peer := agentControl(t, "cancel-peer")
	connection := connectAgentControls(t, source, peer)
	from := protocol.AgentRef{NodeID: "node", Kind: "assistant", AgentID: "main"}
	record := tasks.Record{AssistantID: "main", Request: protocol.CreateTask{RequestID: "cancel-request", Text: "Remote work"}, Info: protocol.TaskInfo{ID: "cancel-task", AssistantID: "main", Phase: protocol.TaskRunning, AgentLink: &protocol.AgentLinkOrigin{LinkID: connection.ID, NodeID: "cancel-peer", AssistantID: "main"}}, Execution: &tasks.Execution{Request: agentlink.TaskRequest{From: from, RequestID: "cancel-request"}, Receipt: agentlink.Task{ID: "accepted-task", RequestID: "cancel-request", RunID: "original-run", State: "running"}}}
	if err := backend.s.Tasks.AcceptExecution(ctx, record); err != nil {
		t.Fatal(err)
	}
	capabilities := backend.s.taskExecutionCapabilities(ctx, &record)
	if !capabilities.Supports(agentlink.TaskCancel) || capabilities.Supports(agentlink.TaskInput) || capabilities.Scope != "caller" {
		t.Fatal(capabilities)
	}
	// A type label cannot grant an executor capability absent from the directory.
	record.Execution.Request.Executor = "codex"
	if backend.s.taskExecutionCapabilities(ctx, &record).Supports(agentlink.TaskInput) {
		t.Fatal("invented Codex input capability")
	}
	record.Execution.Request.Executor = ""
	if err := backend.s.agentLinks.service.Disconnect(ctx, connection.ID); err != nil {
		t.Fatal(err)
	}
	if err := backend.s.StopTask(ctx, "main", record.Info.ID, "cancel"); !errors.Is(err, agentlink.ErrDisconnected) {
		t.Fatal(err)
	}
	backend.s.syncTaskExecutions(ctx)
	options := backend.s.options
	if err := backend.s.Close(); err != nil {
		t.Fatal(err)
	}
	options.BootID = "cancel-restarted"
	restored, err := Open(options)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	stored, err := restored.Tasks.Get(ctx, "main", record.Info.ID)
	if err != nil || !stored.Execution.StopRequested || stored.Execution.Receipt.ID != "accepted-task" || stored.Execution.Receipt.RunID != "original-run" || stored.Info.Phase != protocol.TaskRunning || stored.Result != nil {
		t.Fatal(stored, err)
	}
}

func TestLinkedReceiptsKeepCancellationActiveAndFastContinuationVisible(t *testing.T) {
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

func TestLinkedSubmissionRejectsLocalConfigurationBeforeAdmission(t *testing.T) {
	backend, _ := linkBackendFixture(t)
	source := &agentControlFixture{service: backend.s, endpoint: controlPeerEndpoint(backend.s)}
	peer := agentControl(t, "submission-peer")
	connection := connectAgentControls(t, source, peer)
	profile, model := "local-profile", "local-model"
	skills := []string{"local-skill"}
	cases := []protocol.CreateTask{
		{MachineID: "node"}, {Profile: &profile}, {Model: &model}, {Skills: &skills},
		{Executor: "codex", Workspace: protocol.TaskWorkspace{Kind: "directory", ProjectDir: "/peer/project"}},
		{Requirements: protocol.TaskRequirements{Tools: true}},
		{Workspace: protocol.TaskWorkspace{Kind: "worktree", ProjectDir: "/peer/project"}},
	}
	ctx := context.Background()
	for i, request := range cases {
		request.RequestID = fmt.Sprintf("rejected-%d", i)
		request.Text = "Remote work"
		request.LinkID = connection.ID
		if _, err := backend.s.CreateTask(ctx, "main", request); !errors.Is(err, agentlink.ErrUnsupported) {
			t.Fatal(request, err)
		}
	}
	var submissions int
	if err := backend.s.Tasks.DB().QueryRow("SELECT COUNT(*) FROM tasks").Scan(&submissions); err != nil || submissions != 0 {
		t.Fatal("unsupported configuration left queued work", submissions, err)
	}
}

func TestLinkedGuidanceCannotUpgradeReferenceOrForeignSender(t *testing.T) {
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
