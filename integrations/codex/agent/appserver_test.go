package agent

import (
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
