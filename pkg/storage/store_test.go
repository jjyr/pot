package storage

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"

	"github.com/jjyr/pot/pkg/protocol"
)

func testStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "thread.sqlite")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}
func message(id string, status protocol.MessageStatus) Message {
	return Message{ID: id, ConversationID: "main", Role: protocol.Assistant, Text: "partial", Status: status, CreatedAtMS: Now()}
}
func TestRestartOutboxAndAppendOnlyEvents(t *testing.T) {
	ctx := context.Background()
	s, path := testStore(t)
	m := message("reply", protocol.Streaming)
	request := "request-1"
	m.RequestID = &request
	if _, err := s.SaveWithMetadata(ctx, m, map[string]string{"checkpoint": "ready"}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	var err error
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	loaded, err := s.Duplicate(ctx, request)
	if err != nil || loaded.Status != protocol.Failed || loaded.Text != "partial" {
		t.Fatalf("recovery: %+v %v", loaded, err)
	}
	events, err := s.Events(ctx, 0)
	if err != nil || len(events) != 2 || events[0].Message.Status != protocol.Streaming || events[1].Message.Status != protocol.Failed {
		t.Fatalf("append-only history changed: %+v %v", events, err)
	}
	m.Status = protocol.Complete
	for i := 0; i < 2; i++ {
		if _, err := s.Save(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"task-report-one", "synthetic"} {
		m.ID = id
		m.RequestID = nil
		if _, err := s.SaveNonNotifying(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	out, err := s.Notifications(ctx, 0)
	if err != nil || len(out) != 1 || out[0].Message.ID != "reply" {
		t.Fatalf("outbox: %+v %v", out, err)
	}
	if value, ok, err := s.Get(ctx, "checkpoint"); err != nil || !ok || value != "ready" {
		t.Fatal(value, ok, err)
	}
}
func TestAtomicSaveRollbackAndContextCorruption(t *testing.T) {
	ctx := context.Background()
	s, _ := testStore(t)
	r := "unique"
	m := message("one", protocol.Complete)
	m.RequestID = &r
	if _, err := s.Save(ctx, m); err != nil {
		t.Fatal(err)
	}
	m.ID = "two"
	if _, err := s.SaveWithMetadata(ctx, m, map[string]string{"should-not-commit": "bad"}); err == nil {
		t.Fatal("duplicate accepted")
	}
	if _, ok, err := s.Get(ctx, "should-not-commit"); err != nil || ok {
		t.Fatal(ok, err)
	}
	cursor, _ := s.Cursor(ctx)
	if cursor != 1 {
		t.Fatal(cursor)
	}
	body := json.RawMessage(`{"type":"context","version":2,"messages":[{"role":"assistant","native":{"encrypted":"opaque"}}]}`)
	if err := s.SaveContext(ctx, body); err != nil {
		t.Fatal(err)
	}
	if loaded, err := s.LoadContext(ctx); err != nil || string(loaded) != string(body) {
		t.Fatal(string(loaded), err)
	}
	if err := s.SaveContext(ctx, json.RawMessage(`{"messages":[]}`)); err == nil {
		t.Fatal("invalid context accepted")
	}
	if err := s.Set(ctx, "thread.context", "corrupt"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LoadContext(ctx); err == nil {
		t.Fatal("corrupt context silently replaced")
	}
	s.Set(ctx, "thread", "{}")
	if _, err := s.db.Exec("DELETE FROM metadata WHERE key='thread.context'"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LoadContext(ctx); err == nil {
		t.Fatal("missing context silently replaced")
	}
}
func TestInstructionRevisionCASAcrossHandlesAndABA(t *testing.T) {
	ctx := context.Background()
	s, path := testStore(t)
	original, err := s.Instructions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, store := range []*Store{s, second} {
		wg.Add(1)
		go func(db *Store) {
			defer wg.Done()
			_, err := db.SaveInstructions(ctx, "changed", original.Revision)
			results <- err
		}(store)
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("CAS had %d winners", success)
	}
	current, _ := s.Instructions(ctx)
	reverted, err := s.SaveInstructions(ctx, "", current.Revision)
	if err != nil || reverted.Revision == original.Revision {
		t.Fatal(reverted, err)
	}
	if _, err := s.SaveInstructions(ctx, "stale", original.Revision); err == nil {
		t.Fatal("ABA write accepted")
	}
}
func TestScopedMailboxRecoveryAndDedup(t *testing.T) {
	ctx := context.Background()
	s, path := testStore(t)
	body := json.RawMessage(`{"text":"hello"}`)
	first, err := s.Enqueue(ctx, "assistant-a", "mail-1", body)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := s.Enqueue(ctx, "assistant-a", "mail-1", body)
	if err != nil || duplicate.Sequence != first.Sequence {
		t.Fatal(duplicate, err)
	}
	if _, err := s.Enqueue(ctx, "assistant-a", "mail-1", json.RawMessage(`{"text":"changed"}`)); err == nil {
		t.Fatal("conflicting receipt accepted")
	}
	if err := s.TransitionMail(ctx, "assistant-a", "mail-1", "queued", "processing"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	history, err := s.Mail(ctx, "assistant-a", 0)
	if err != nil || len(history) != 1 || history[0].State != "interrupted" {
		t.Fatal(history, err)
	}
	other, err := s.Mail(ctx, "assistant-b", 0)
	if err != nil || len(other) != 0 {
		t.Fatal(other, err)
	}
	if err := s.TransitionMail(ctx, "assistant-a", "mail-1", "queued", "processing"); err == nil {
		t.Fatal("uncertain attempt replayed")
	}
}

func TestRestartRetainsToolDetailsWithoutDeclaringSuccess(t *testing.T) {
	ctx := context.Background()
	s, path := testStore(t)
	m := Message{ID: "tool-message", ConversationID: "main", Role: protocol.Tool, Status: protocol.Streaming, CreatedAtMS: Now(), Tool: &protocol.ToolCall{TurnID: "turn", ID: "call", Name: "exec_command", Args: json.RawMessage(`{"cmd":"pwd"}`), Phase: "running", StartedAtMS: 1, Output: []protocol.ToolOutput{{Stream: "stdout", Text: "partial output"}}}}
	if _, err := s.Save(ctx, m); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	saved, err := reopened.Message(ctx, m.ID)
	if err != nil || saved == nil || saved.Tool == nil || saved.Tool.Phase != "unknown" || saved.Tool.Output[0].Text != "partial output" || string(saved.Tool.Args) != `{"cmd":"pwd"}` || saved.Status != protocol.Failed {
		t.Fatal(saved, err)
	}
}
