package control

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/jjyr/pot/internal/tasks"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/protocol"
)

func TestMCPCutoverHistoryNeverReplaysUnfinishedLegacyWork(t *testing.T) {
	fixture, _ := linkBackendFixture(t)
	s := fixture.s
	ctx := t.Context()
	target := agentlink.AssistantTarget("old-node", "old-assistant")
	history := agentlink.HistoricalConnection{SchemaVersion: 1, ID: "old-link", State: "unsupported", LastKnownState: "connecting", Peer: agentlink.Identity{Address: agentlink.NodeTarget("old-node"), Name: "Old", Adapter: "codex"}, Directory: &agentlink.Directory{NodeID: "old-node", Name: "Old", Adapter: "codex", Targets: []agentlink.Endpoint{{Identity: agentlink.Identity{Address: target, Name: "Old assistant", Adapter: "codex"}, Capabilities: agentlink.Description{Version: 1, Scope: "peer", Operations: []agentlink.Operation{agentlink.ChatRead, agentlink.ChatSend, agentlink.TaskSubmit}}}}}}
	raw, _ := json.Marshal(history)
	if _, e := s.integrationDB.Exec("INSERT INTO agent_link_history VALUES(?,?)", history.ID, string(raw)); e != nil {
		t.Fatal(e)
	}
	record := tasks.Record{AssistantID: "main", Request: protocol.CreateTask{RequestID: "old-request", Text: "Old accepted work"}, Info: protocol.TaskInfo{ID: "old-task", AssistantID: "main", Phase: protocol.TaskRunning, AgentLink: &protocol.AgentLinkOrigin{LinkID: "old-link", NodeID: "old-node"}}, Execution: &tasks.Execution{Request: agentlink.TaskRequest{From: agentlink.AssistantTarget(s.options.RuntimeID, "main"), Target: target, RequestID: "old-request", Text: "Old accepted work"}}}
	if e := s.Tasks.AcceptExecution(ctx, record); e != nil {
		t.Fatal(e)
	}
	var before, after string
	if e := s.Tasks.DB().QueryRow("SELECT body FROM tasks WHERE id='old-task'").Scan(&before); e != nil {
		t.Fatal(e)
	}
	s.syncTaskExecutions(ctx)
	if e := s.StopTask(ctx, "main", "old-task", "cancel"); !errors.Is(e, agentlink.ErrUnsupported) {
		t.Fatal("legacy cancel changed intent", e)
	}
	if e := s.Tasks.DB().QueryRow("SELECT body FROM tasks WHERE id='old-task'").Scan(&after); e != nil || after != before {
		t.Fatal("legacy unfinished work changed", e)
	}
	caps, e := s.AgentCapabilities(ctx, "agent-link:old-link:old-assistant")
	if e != nil || !caps.Supports(agentlink.ChatRead) || caps.Supports(agentlink.ChatSend) || caps.Supports(agentlink.TaskSubmit) || caps.Supports(agentlink.TaskCancel) {
		t.Fatal("history became executable", caps, e)
	}
	message := protocol.Envelope{Kind: "message", ID: "legacy-mail", From: target, To: agentlink.AssistantTarget(s.options.RuntimeID, "main"), ConversationID: "d115b4cf-5bfd-4b5a-812c-1dfb56555a9e", Text: "Old queued correspondence"}
	if _, e := s.Mailbox.Deliver(ctx, message); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Tasks.DB().Exec("INSERT INTO retired_agent_link_work VALUES('message',?)", message.ID); e != nil {
		t.Fatal(e)
	}
	candidates, e := s.Mailbox.Candidates(ctx, "main")
	if e != nil {
		t.Fatal(e)
	}
	for _, candidate := range candidates {
		if candidate.ID == message.ID {
			t.Fatal("old remote mailbox resumed")
		}
	}
	var state string
	if e = s.Tasks.DB().QueryRow("SELECT state FROM agent_messages WHERE id=?", message.ID).Scan(&state); e != nil || state != "queued" {
		t.Fatal("old mailbox last state rewritten", state, e)
	}
}
