package agenthost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"
)

func TestApprovalPreservesExactFileAndNetworkScope(t *testing.T) {
	for _, method := range []string{"item/fileChange/requestApproval", "item/commandExecution/requestApproval"} {
		t.Run(method, func(t *testing.T) {
			var output bytes.Buffer
			a := &appTurn{input: nopWriteCloser{&output}, ctx: context.Background(), done: make(chan struct{}), reviews: map[string]context.CancelFunc{}, result: TurnResult{ThreadID: "thread", TurnID: "turn"}}
			if method == "item/fileChange/requestApproval" {
				a.notification(rpcMessage{Method: "item/started", Params: json.RawMessage(`{"threadId":"thread","item":{"id":"item","type":"fileChange","changes":[{"path":"/work/config","diff":"+setting"}]}}`)})
			}
			a.events.Review = func(ctx context.Context, got string, raw json.RawMessage) (string, error) {
				var params map[string]json.RawMessage
				if err := json.Unmarshal(raw, &params); err != nil {
					t.Error(err)
				}
				if got != method {
					t.Error(got)
				}
				if method == "item/fileChange/requestApproval" && !bytes.Contains(params["proposedChanges"], []byte("+setting")) {
					t.Error("proposed file diff omitted")
				}
				if method == "item/commandExecution/requestApproval" && !bytes.Contains(params["networkApprovalContext"], []byte("example.com")) {
					t.Error("network scope omitted")
				}
				return "allow_once", nil
			}
			a.approval(rpcMessage{ID: json.RawMessage(`"request"`), Method: method, Params: json.RawMessage(`{"threadId":"thread","turnId":"turn","itemId":"item","networkApprovalContext":{"host":"example.com","protocol":"https"},"availableDecisions":["accept","decline"]}`)})
			a.reviewWG.Wait()
			if !strings.Contains(output.String(), `"decision":"accept"`) {
				t.Fatal(output.String())
			}
		})
	}
}

// Even if an older server emits automatic-review events despite opt-out, they
// must never become human requests or leak review rationale into the task reply.
func TestAutomaticReviewNotificationsStayInsideCodex(t *testing.T) {
	for _, status := range []string{"approved", "denied", "timedOut"} {
		t.Run(status, func(t *testing.T) {
			a := &appTurn{done: make(chan struct{}), readerDone: make(chan struct{}), pending: map[int]chan rpcMessage{}, result: TurnResult{ThreadID: "thread"}, events: turnEvents{Review: func(context.Context, string, json.RawMessage) (string, error) {
				t.Fatal("automatic review requested human approval")
				return "", nil
			}}}
			var stream strings.Builder
			enc := json.NewEncoder(&stream)
			for _, method := range []string{"item/autoApprovalReview/started", "item/autoApprovalReview/completed"} {
				if err := enc.Encode(map[string]any{"method": method, "params": map[string]any{"threadId": "thread", "turnId": "turn", "review": map[string]string{"status": status, "rationale": "private automatic review"}}}); err != nil {
					t.Fatal(err)
				}
			}
			for _, event := range []map[string]any{
				{"method": "item/completed", "params": map[string]any{"threadId": "thread", "item": map[string]string{"type": "agentMessage", "phase": "final_answer", "text": "task result"}}},
				{"method": "turn/completed", "params": map[string]any{"threadId": "thread", "turn": map[string]string{"id": "turn", "status": "completed"}}},
			} {
				if err := enc.Encode(event); err != nil {
					t.Fatal(err)
				}
			}
			a.read(strings.NewReader(stream.String()))
			if a.result.Status != "completed" || a.result.Text != "task result\n" || a.denied {
				t.Fatal(a.result)
			}
		})
	}
}

func TestThreadOpenFailureRetainsRPCDiagnostic(t *testing.T) {
	for _, tc := range []struct{ name, method, thread, code, detail string }{
		{"start", "start", "", "codex_thread_open_failed", "fixture thread unavailable"},
		{"resume", "resume", "missing-thread", "codex_thread_open_failed", "fixture thread unavailable"},
		{"writer-conflict", "resume", "busy-thread", "codex_thread_in_use", "already has an active writer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workspace := t.TempDir()
			session := Session{Binary: fakeBinary, Workspace: workspace, Mode: "read-only"}
			worker, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			session.Worker = []string{worker}
			if tc.method == "resume" {
				session.ThreadID = tc.thread
			} else {
				session.Workspace = filepath.Join(workspace, "reject-thread-start")
				if err := os.Mkdir(session.Workspace, 0700); err != nil {
					t.Fatal(err)
				}
			}
			result := runTurn(context.Background(), session, "must not run", turnEvents{
				Thread:  func(string) error { t.Error("persisted a rejected thread"); return nil },
				Started: func(string) error { t.Error("started a turn after rejection"); return nil },
			})
			if result.Status != "failed" || result.Text != "" || result.ThreadID != session.ThreadID || !strings.HasPrefix(result.Error, tc.code+": ") || !strings.Contains(result.Error, "codex_rpc_rejected_thread_"+tc.method) || !strings.Contains(result.Error, "-32600") || !strings.Contains(result.Error, tc.detail) {
				t.Fatal(result)
			}
		})
	}
}

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
