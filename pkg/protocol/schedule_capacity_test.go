package protocol

import (
	"strings"
	"testing"
)

func TestScheduledInputCapacityKeepsSubmissionAndMailBoundaries(t *testing.T) {
	request := CreateTask{RequestID: "request", Text: strings.Repeat("a", 65536)}
	if err := request.Validate(); err != nil {
		t.Fatal(err)
	}
	request.Text += "a"
	if err := request.Validate(); err == nil {
		t.Fatal("ordinary submission limit widened")
	}
	request.Text = strings.Repeat("a", 524288+1024)
	if err := request.ValidateScheduled(); err != nil {
		t.Fatal(err)
	}
	request.Text += "a"
	if err := request.ValidateScheduled(); err == nil {
		t.Fatal("scheduled input beyond limit accepted")
	}

	envelope := Envelope{ID: "message", Kind: "input", Owner: "assistant", RequestID: "request",
		From:           AgentRef{NodeID: "node", Kind: "assistant", AgentID: "assistant"},
		To:             AgentRef{NodeID: "node", Kind: "task", AgentID: "task"},
		ConversationID: "12345678-1234-4234-8234-123456789abc", Text: request.Text[:len(request.Text)-1]}
	if err := envelope.Validate(); err != nil {
		t.Fatal(err)
	}
	envelope.Text += "a"
	if err := envelope.Validate(); err == nil {
		t.Fatal("initial envelope beyond limit accepted")
	}
	envelope.Text = strings.Repeat("a", 65537)
	for _, kind := range []string{"guidance", "user_guidance", "reference"} {
		envelope.Kind = kind
		if err := envelope.Validate(); err == nil {
			t.Fatalf("%s mail limit widened", kind)
		}
	}
	envelope.Kind, envelope.Owner, envelope.RequestID = "message", "", ""
	envelope.To.Kind = "assistant"
	if err := envelope.Validate(); err == nil {
		t.Fatal("correspondence limit widened")
	}
	envelope.Text = envelope.Text[:65536]
	if err := envelope.Validate(); err != nil {
		t.Fatal(err)
	}
}
