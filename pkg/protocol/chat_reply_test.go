package protocol

import "testing"

func TestChatReplyLifecycleUsesExecutionRatherThanOutputCompletion(t *testing.T) {
	messages := []Message{{ID: "input", Role: User}, {ID: "output", Role: Assistant, ReplyTo: "input", Status: Complete, Text: "intermediate"}}
	for _, test := range []struct {
		turn  *TurnState
		state string
	}{
		{&TurnState{UserMessageID: "input"}, "running"},
		{&TurnState{UserMessageID: "input", Paused: true}, "paused"},
		{&TurnState{UserMessageID: "input", State: "waiting"}, "waiting"},
		{&TurnState{UserMessageID: "input", Cancelling: true}, "cancelling"},
		{&TurnState{UserMessageID: "input", State: "unknown"}, "unknown"},
		{nil, "completed"},
	} {
		rows := ProjectChatReplies(messages, test.turn)
		if len(rows) != 1 || rows[0].State != test.state {
			t.Fatalf("%s: %+v", test.state, rows)
		}
	}
	messages[1].Status = Failed
	messages[1].Error = "preparation failed"
	rows := ProjectChatReplies(messages, nil)
	if rows[0].State != "failed" || rows[0].Error != "preparation failed" {
		t.Fatal(rows)
	}
	rows = ProjectChatReplies([]Message{messages[1], messages[0]}, nil)
	if len(rows) != 1 || rows[0].State != "failed" {
		t.Fatal("context dependency order changed terminal state", rows)
	}
	messages = append(messages, Message{ID: "retry-output", Role: Assistant, ReplyTo: "input", Status: Complete, Text: "recovered"})
	rows = ProjectChatReplies(messages, nil)
	if rows[0].State != "completed" || rows[0].Error != "" {
		t.Fatal("a successful retry retained the prior failed outcome", rows)
	}
}
