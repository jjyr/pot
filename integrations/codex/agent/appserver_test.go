package agent

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"
)

func TestAppServerReadFailureIsDistinctFromProcessExit(t *testing.T) {
	for _, tc := range []struct {
		name   string
		reader io.Reader
		want   string
	}{
		{"eof", strings.NewReader(""), "codex_process_ended_outcome_unknown"},
		{"read-error", iotest.ErrReader(errors.New("read failed")), "codex_jsonl_read_failed_outcome_unknown"},
		{"oversized-event", strings.NewReader(strings.Repeat("x", 4*1024*1024+1)), "codex_jsonl_read_failed_outcome_unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &appTurn{done: make(chan struct{}), readerDone: make(chan struct{})}
			a.read(tc.reader)
			if a.result.Status != "failed" || a.result.Error != tc.want {
				t.Fatal(a.result)
			}
		})
	}
}

func TestAppServerKeepsOnlyAnswerAndStopsAtTurnCompletion(t *testing.T) {
	a := &appTurn{done: make(chan struct{}), readerDone: make(chan struct{}), pending: map[int]chan rpcMessage{}, result: TurnResult{ThreadID: "thread"}}
	var stream strings.Builder
	enc := json.NewEncoder(&stream)
	for _, item := range []map[string]string{
		{"type": "agentMessage", "phase": "commentary", "text": "checking"},
		{"type": "agentMessage", "phase": "partial_answer", "text": "preliminary"},
		{"type": "agentMessage", "phase": "final_answer", "text": "answer"},
	} {
		if err := enc.Encode(map[string]any{"method": "item/completed", "params": map[string]any{"threadId": "thread", "item": item}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := enc.Encode(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": "thread", "turn": map[string]string{"id": "turn", "status": "completed"}}}); err != nil {
		t.Fatal(err)
	}
	// Later notifications cannot change the answer of the completed turn.
	if err := enc.Encode(map[string]any{"method": "item/completed", "params": map[string]any{"threadId": "thread", "item": map[string]string{"type": "agentMessage", "phase": "final_answer", "text": "later answer"}}}); err != nil {
		t.Fatal(err)
	}
	a.read(strings.NewReader(stream.String()))
	if a.result.Status != "completed" || a.result.Text != "answer\n" {
		t.Fatalf("turn result included intermediate or later output: %+v", a.result)
	}
}
