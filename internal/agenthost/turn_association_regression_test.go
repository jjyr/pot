package agenthost

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

// TurnStarted/TurnCompleted require threadId + turn; ItemCompleted additionally
// requires turnId in the installed App Server v2 schema.
func TestAppTurnIgnoresStaleStartedRebinding(t *testing.T) {
	a := &appTurn{done: make(chan struct{}), result: TurnResult{ThreadID: "thread", TurnID: "current"}}
	a.notification(rpcMessage{Method: "turn/started", Params: json.RawMessage(`{"threadId":"thread","turn":{"id":"old","items":[],"status":"inProgress"}}`)})
	a.notification(failedTurnNotification(t, "thread", "old", "contextWindowExceeded"))
	if a.result.TurnID != "current" || a.result.Status != "" {
		t.Fatalf("stale started notification rebound or completed current execution: %+v", a.result)
	}
	a.notification(failedTurnNotification(t, "thread", "current", "unauthorized"))
	if a.result.Status != "failed" || a.result.Error != "codex_turn_failed: unauthorized" {
		t.Fatalf("current failure was not classified: %+v", a.result)
	}
}

func TestAppTurnResponseThenCompletionBeforeRPCConsumer(t *testing.T) {
	// No consumer runs until read returns: this is a deterministic schedule
	// permitted by rpc's buffered response channel. A queued turn/start response
	// must establish its turn binding before the next JSONL lifecycle event.
	response := make(chan rpcMessage, 1)
	a := &appTurn{pending: map[int]chan rpcMessage{1: response}, pendingMethods: map[int]string{1: "turn/start"}, starting: 1, done: make(chan struct{}), readerDone: make(chan struct{}), result: TurnResult{ThreadID: "thread"}}
	var input strings.Builder
	encoder := json.NewEncoder(&input)
	if err := encoder.Encode(map[string]any{"id": 1, "result": map[string]any{"turn": map[string]any{"id": "current", "items": []any{}, "status": "inProgress"}}}); err != nil {
		t.Fatal(err)
	}
	notification := failedTurnNotification(t, "thread", "current", "unauthorized")
	if err := encoder.Encode(map[string]any{"method": notification.Method, "params": notification.Params}); err != nil {
		t.Fatal(err)
	}
	a.read(strings.NewReader(input.String()))
	if a.result.TurnID != "current" || a.result.Status != "failed" || a.result.Error != "codex_turn_failed: unauthorized" {
		t.Fatalf("terminal classification depended on RPC consumer scheduling: %+v", a.result)
	}
}

func TestAppTurnRejectsOtherTurnFinalAnswer(t *testing.T) {
	a := &appTurn{done: make(chan struct{}), result: TurnResult{ThreadID: "thread", TurnID: "current"}}
	for _, turn := range []string{"old", "current"} {
		params, err := json.Marshal(map[string]any{"threadId": "thread", "turnId": turn, "completedAtMs": 1, "item": map[string]string{"id": turn + "-item", "type": "agentMessage", "phase": "final_answer", "text": turn + " answer"}})
		if err != nil {
			t.Fatal(err)
		}
		a.notification(rpcMessage{Method: "item/completed", Params: params})
	}
	a.notification(rpcMessage{Method: "turn/completed", Params: json.RawMessage(`{"threadId":"thread","turn":{"id":"current","items":[],"status":"completed"}}`)})
	if a.result.Status != "completed" || a.result.Text != "current answer\n" {
		t.Fatalf("another turn polluted the final answer: %+v", a.result)
	}
}

func TestAppTurnRequiresUnambiguousAssociation(t *testing.T) {
	for _, params := range []string{
		`{"threadId":"foreign","threadId":"thread","turn":{"id":"current","status":"completed"}}`,
		`{"threadId":"thread","turn":{"id":"old","id":"current","status":"completed"}}`,
		`{"threadId":"thread","turn":{"id":"","status":"completed"}}`,
		`{"turn":{"id":"current","status":"completed"}}`,
		`{"threadId":"thread","turn":null}`,
	} {
		a := &appTurn{done: make(chan struct{}), result: TurnResult{ThreadID: "thread", TurnID: "current"}}
		a.notification(rpcMessage{Method: "turn/completed", Params: json.RawMessage(params)})
		if a.result.Status != "" {
			t.Fatalf("ambiguous event completed turn: %s %+v", params, a.result)
		}
	}
	for _, params := range []string{
		`{"threadId":"thread","turnId":"old","turnId":"current","item":{"type":"agentMessage","text":"wrong"}}`,
		`{"threadId":"thread","item":{"type":"agentMessage","text":"wrong"}}`,
	} {
		a := &appTurn{done: make(chan struct{}), result: TurnResult{ThreadID: "thread", TurnID: "current"}}
		a.notification(rpcMessage{Method: "item/completed", Params: json.RawMessage(params)})
		if a.result.Text != "" {
			t.Fatal(a.result)
		}
	}
}

func TestAppTurnTerminalResultIsImmutable(t *testing.T) {
	a := &appTurn{done: make(chan struct{}), result: TurnResult{ThreadID: "thread", TurnID: "current"}}
	a.finish("completed", "")
	a.notification(rpcMessage{Method: "item/completed", Params: json.RawMessage(`{"threadId":"thread","turnId":"current","item":{"type":"agentMessage","text":"late"}}`)})
	a.notification(rpcMessage{Method: "item/started", Params: json.RawMessage(`{"threadId":"thread","turnId":"current","item":{"id":"late","type":"fileChange"}}`)})
	a.notification(rpcMessage{Method: "turn/started", Params: json.RawMessage(`{"threadId":"thread","turn":{"id":"old"}}`)})
	if a.result.Text != "" || a.result.TurnID != "current" || len(a.fileChanges) != 0 {
		t.Fatal(a.result)
	}
}

func TestAppTurnBufferBounds(t *testing.T) {
	for _, large := range []bool{false, true} {
		a := &appTurn{done: make(chan struct{}), starting: 1, pending: map[int]chan rpcMessage{1: make(chan rpcMessage, 1)}, result: TurnResult{ThreadID: "thread"}}
		text := "x"
		count := 65
		if large {
			text = strings.Repeat("x", maxAppServerMessage/2)
			count = 2
		}
		params, _ := json.Marshal(map[string]any{"threadId": "thread", "turnId": "current", "item": map[string]string{"type": "agentMessage", "text": text}})
		for i := 0; i < count; i++ {
			a.bufferBeforeStart(rpcMessage{Method: "item/completed", Params: params})
		}
		if a.result.Error != "codex_turn_event_buffer_exceeded_outcome_unknown" {
			t.Fatal(a.result)
		}
	}
}

func TestAppTurnBeforeResponseReplayAndStartedOnce(t *testing.T) {
	started := 0
	a := &appTurn{done: make(chan struct{}), starting: 1, pending: map[int]chan rpcMessage{1: make(chan rpcMessage, 1)}, result: TurnResult{ThreadID: "thread"}, events: turnEvents{Started: func(id string) error {
		started++
		if id != "current" {
			t.Fatal(id)
		}
		return nil
	}}}
	for _, m := range []rpcMessage{
		{Method: "turn/started", Params: json.RawMessage(`{"threadId":"thread","turn":{"id":"old"}}`)},
		{Method: "turn/started", Params: json.RawMessage(`{"threadId":"thread","turn":{"id":"current"}}`)},
		{Method: "item/completed", Params: json.RawMessage(`{"threadId":"thread","turnId":"current","item":{"type":"agentMessage","text":"answer"}}`)},
		{Method: "turn/completed", Params: json.RawMessage(`{"threadId":"thread","turn":{"id":"current","status":"completed"}}`)},
	} {
		if !a.bufferBeforeStart(m) {
			t.Fatal("early event not buffered")
		}
	}
	a.bindStartResponse(1, rpcMessage{Result: json.RawMessage(`{"turn":{"id":"current","status":"inProgress","extra":true}}`)})
	a.notification(rpcMessage{Method: "turn/started", Params: json.RawMessage(`{"threadId":"thread","turn":{"id":"current"}}`)})
	if started != 1 || a.result.Status != "completed" || a.result.Text != "answer\n" || a.starting != 0 {
		t.Fatalf("started=%d result=%+v", started, a.result)
	}
}

func TestAppTurnSubprocessOrderingAndBufferCleanup(t *testing.T) {
	for _, tc := range []struct {
		prompt, status, text, diagnostic string
		started, reviews                 int
	}{
		{"failure-without-started", "failed", "", "codex_turn_failed: unauthorized", 1, 0},
		{"early-events", "completed", "early answer\n", "", 1, 0},
		{"early-approval", "completed", "approved operation completed\n", "", 1, 1},
		{"early-overflow-count", "failed", "", "codex_turn_event_buffer_exceeded_outcome_unknown", 0, 0},
		{"early-overflow-bytes", "failed", "", "codex_turn_event_buffer_exceeded_outcome_unknown", 0, 0},
	} {
		t.Run(tc.prompt, func(t *testing.T) {
			started, reviews := 0, 0
			worker, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			result := runTurn(t.Context(), Session{Binary: fakeBinary, Workspace: t.TempDir(), Mode: "read-only", Worker: []string{worker}}, tc.prompt, turnEvents{
				Started: func(id string) error {
					started++
					if id != "turn-1" {
						return fmt.Errorf("wrong id")
					}
					return nil
				},
				Review: func(context.Context, string, json.RawMessage) (string, error) { reviews++; return "allow_once", nil },
			})
			if result.Status != tc.status || result.Text != tc.text || result.Error != tc.diagnostic || result.CleanupUncertain || started != tc.started || reviews != tc.reviews {
				t.Fatalf("result=%+v started=%d reviews=%d", result, started, reviews)
			}
		})
	}
}

func TestAppTurnRetiredStartResponseCannotBind(t *testing.T) {
	started := 0
	a := &appTurn{done: make(chan struct{}), pending: map[int]chan rpcMessage{}, result: TurnResult{ThreadID: "thread"}, events: turnEvents{Started: func(string) error { started++; return nil }}}
	a.bindStartResponse(1, rpcMessage{Result: json.RawMessage(`{"turn":{"id":"late"}}`)})
	if a.result.TurnID != "" || started != 0 {
		t.Fatalf("retired response bound %+v started=%d", a.result, started)
	}
}
