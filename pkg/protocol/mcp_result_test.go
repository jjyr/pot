package protocol

import "testing"

func TestReceiverResultRetainsActualNonUUIDRun(t *testing.T) {
	run := "native-sha256-run-identifier"
	result := TaskResultEvent{TaskID: "dispatch", RunID: run, Title: "Native result", Phase: TaskComplete, Text: "done", Origin: TaskOrigin{Kind: "manual"}}
	envelope := Envelope{ID: "native-result", Kind: "task_result", From: AgentRef{NodeID: "pot", Kind: "runtime", AgentID: "agent-link"}, To: AgentRef{NodeID: "pot", Kind: "assistant", AgentID: "owner"}, ConversationID: run, Text: "done", Result: &result}
	if err := envelope.Validate(); err != nil {
		t.Fatal("actual receiver run rejected", err)
	}
	envelope.ConversationID = "unrelated-run"
	if err := envelope.Validate(); err == nil {
		t.Fatal("unrelated non-UUID conversation accepted")
	}
	envelope.Kind = "message"
	envelope.Result = nil
	envelope.From.Kind = "assistant"
	envelope.ConversationID = run
	if err := envelope.Validate(); err == nil {
		t.Fatal("ordinary correspondence gained non-UUID conversations")
	}
}
