package assistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jjyr/pot/internal/inbox"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

func TestUserBatchRunsOnceAndRetainsReceiptsAfterRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "chat.sqlite")
	var calls, cleanup atomic.Int64
	model := func(_ context.Context, r core.ModelRequest) (io.ReadCloser, error) {
		calls.Add(1)
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.Unmarshal(r.Body, &body); err != nil {
			return nil, err
		}
		if strings.Contains(body.Messages[0].Content, "Summarize background") {
			t.Error("user batch inherited Summary role")
		}
		last := ""
		for _, message := range body.Messages {
			if message.Role == "user" {
				last += message.Content + "\n"
			}
		}
		if strings.Index(last, "first user request") < 0 || strings.Index(last, "second user constraint") < strings.Index(last, "first user request") {
			t.Error("user batch lost original order", last)
		}
		return replySSE("both users handled"), nil
	}
	c := conversationFixture(t, path, "old", model, &cleanup)
	var receipts []protocol.SendMessageAccepted
	for i, text := range []string{"first user request", "second user constraint"} {
		c.mu.Lock()
		receipt, err := c.queueUserLocked(ctx, protocol.SendMessage{RequestID: fmt.Sprintf("user-%d", i), ConversationID: "main", Text: text})
		c.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		receipts = append(receipts, receipt)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	c = conversationFixture(t, path, "new", model, &cleanup)
	if _, err := c.Report(ctx, "pot-result-queued", "must not overtake users"); err == nil {
		t.Fatal("result overtook queued user batch")
	}
	if err := c.StartPendingUsers(ctx); err != nil {
		t.Fatal(err)
	}
	snapshot := waitSnapshot(t, c, func(s protocol.Snapshot) bool { return s.Turn == nil && len(s.Messages) == 3 })
	if calls.Load() != 1 {
		t.Fatal("users were handled in separate turns", calls.Load())
	}
	for i, message := range snapshot.Messages[:2] {
		if message.ID != receipts[i].MessageID || message.Queued || message.Status != protocol.Complete {
			t.Fatal("receipt changed or member not acknowledged", message)
		}
		retry, err := c.Send(ctx, protocol.SendMessage{RequestID: fmt.Sprintf("user-%d", i), ConversationID: "main", Text: message.Text})
		if err != nil || !retry.Duplicate || retry.MessageID != message.ID {
			t.Fatal(retry, err)
		}
	}
}

func TestUserBatchSelectionRollsBackAllMembers(t *testing.T) {
	ctx := context.Background()
	var cleanup, calls atomic.Int64
	c := conversationFixture(t, filepath.Join(t.TempDir(), "chat.sqlite"), "boot", func(context.Context, core.ModelRequest) (io.ReadCloser, error) {
		calls.Add(1)
		return replySSE("done"), nil
	}, &cleanup)
	for i := range 2 {
		c.mu.Lock()
		_, err := c.queueUserLocked(ctx, protocol.SendMessage{RequestID: fmt.Sprintf("user-%d", i), ConversationID: "main", Text: "queued"})
		c.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.store.DB().Exec(`CREATE TRIGGER reject_second BEFORE UPDATE ON messages WHEN NEW.request_id='user-1' BEGIN SELECT RAISE(ABORT,'rejected'); END`); err != nil {
		t.Fatal(err)
	}
	if err := c.StartPendingUsers(ctx); err == nil {
		t.Fatal("partial input batch admitted")
	}
	var count int
	if err := c.store.DB().QueryRow("SELECT COUNT(*) FROM chat_user_inbox").Scan(&count); err != nil || count != 2 || calls.Load() != 0 {
		t.Fatal(count, calls.Load(), err)
	}
	for _, message := range c.messages {
		if !message.Queued {
			t.Fatal("in-memory projection changed after rollback", message)
		}
	}
	if _, err := c.store.DB().Exec("DROP TRIGGER reject_second"); err != nil {
		t.Fatal(err)
	}
	if err := c.StartPendingUsers(ctx); err != nil {
		t.Fatal(err)
	}
	waitSnapshot(t, c, func(s protocol.Snapshot) bool { return s.Turn == nil && len(s.Messages) == 3 })
	if calls.Load() != 1 {
		t.Fatal(calls.Load())
	}
}

func TestUserBatchBoundLeavesOverflowForNextTurn(t *testing.T) {
	ctx := context.Background()
	var cleanup, calls atomic.Int64
	c := conversationFixture(t, filepath.Join(t.TempDir(), "chat.sqlite"), "boot", func(context.Context, core.ModelRequest) (io.ReadCloser, error) {
		calls.Add(1)
		return replySSE("done"), nil
	}, &cleanup)
	for i := range inbox.MaxBatchMessages + 2 {
		c.mu.Lock()
		_, err := c.queueUserLocked(ctx, protocol.SendMessage{RequestID: fmt.Sprintf("user-%d", i), ConversationID: "main", Text: "queued"})
		c.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := c.StartPendingUsers(ctx); err != nil {
		t.Fatal(err)
	}
	s := waitSnapshot(t, c, func(s protocol.Snapshot) bool { return s.Turn == nil && len(s.Messages) == inbox.MaxBatchMessages+4 })
	if calls.Load() != 2 {
		t.Fatal("batch size not bounded", calls.Load())
	}
	for _, m := range s.Messages {
		if m.Role == protocol.User && (m.Queued || m.Status != protocol.Complete) {
			t.Fatal(m)
		}
	}
}

func enqueueResult(t *testing.T, m *Mailbox, id string, owner string) protocol.Envelope {
	t.Helper()
	run := newConversation()
	e := protocol.Envelope{ID: id, Kind: "task_result", From: protocol.AgentRef{NodeID: m.nodeID, Kind: "runtime", AgentID: "runtime"}, To: address(m.nodeID, owner), ConversationID: run, Text: "outcome " + id, Result: &protocol.TaskResultEvent{Origin: protocol.TaskOrigin{Kind: "manual"}, TaskID: "task-" + id, RunID: run, Title: id, Phase: protocol.TaskFailed, Text: "failure " + id}}
	if err := storage.Transaction(context.Background(), m.db, func(tx *sql.Tx) error { return inbox.EnqueueTx(context.Background(), tx, e, "in", "queued") }); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestResultBatchAtomicClaimReleaseAndPerMemberAcknowledgment(t *testing.T) {
	ctx := context.Background()
	m, _ := mailbox(t, "local")
	a := enqueueResult(t, m, "A", "main")
	b := enqueueResult(t, m, "B", "main")
	enqueueResult(t, m, "foreign", "other")
	sendMail(t, m, "peer", address("local", "peer"), address("local", "main"), nil)
	if err := m.ClaimEventBatch(ctx, "main", []string{a.ID, "foreign"}); err == nil {
		t.Fatal("cross-owner result joined batch")
	}
	if err := m.ClaimEventBatch(ctx, "main", []string{a.ID, "peer"}); err == nil {
		t.Fatal("correspondence joined result batch")
	}
	if err := m.ClaimEventBatch(ctx, "main", []string{a.ID, b.ID}); err != nil {
		t.Fatal("failed batch claim did not roll back", err)
	}
	late := enqueueResult(t, m, "late", "main")
	if err := m.ReleaseEvent(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.ObserveTaskResult(ctx, "main", "explicit", a.Result.TaskID); err != nil {
		t.Fatal(err)
	}
	if err := m.FinishChat(ctx, "main", "explicit", "", true); err != nil {
		t.Fatal(err)
	}
	if err := m.ClaimEventBatch(ctx, "main", []string{b.ID}); err != nil {
		t.Fatal(err)
	}
	if err := m.FinishChat(ctx, "main", "pot-result-B", b.ID, true); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{a.ID, b.ID} {
		var state string
		if err := m.db.QueryRow("SELECT state FROM agent_messages WHERE id=?", id).Scan(&state); err != nil || state != "processed" {
			t.Fatal(id, state, err)
		}
	}
	var state string
	if err := m.db.QueryRow("SELECT state FROM agent_messages WHERE id=?", late.ID).Scan(&state); err != nil || state != "queued" {
		t.Fatal("later result acknowledged by earlier batch", state, err)
	}
}

func TestPeerBatchGroupsFullAddressAcrossWireConversations(t *testing.T) {
	ctx := context.Background()
	m, _ := mailbox(t, "local")
	from, to := address("local", "peer"), address("local", "main")
	a := sendMail(t, m, "a", from, to, nil)
	sendMail(t, m, "other", address("local", "another"), to, nil)
	b := sendMail(t, m, "b", from, to, nil)
	enqueueResult(t, m, "task", "main")
	batch, err := m.ClaimMailBatch(ctx, a)
	if err != nil || len(batch.Messages) != 2 || batch.Messages[1].ID != b.ID || a.ConversationID == b.ConversationID {
		t.Fatal(batch, err)
	}
	if CorrespondenceKey(a) != CorrespondenceKey(b) {
		t.Fatal("same peer split by wire conversation")
	}
	remote := protocol.Envelope{ID: "remote", Kind: "message", From: address("remote", "peer"), To: to, ConversationID: newConversation(), Text: "remote peer"}
	deliver(t, m, remote)
	if CorrespondenceKey(a) == CorrespondenceKey(remote) {
		t.Fatal("same name across Controls merged")
	}
	if err = m.Finish(ctx, batch.ID, true, "done"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{a.ID, b.ID} {
		var state string
		if err = m.db.QueryRow("SELECT state FROM agent_messages WHERE id=?", id).Scan(&state); err != nil || state != "processed" {
			t.Fatal(state, err)
		}
	}
	other, err := m.Next(ctx, "main")
	if err != nil || other == nil || other.ID != "other" {
		t.Fatal(other, err)
	}
}

func TestPeerBatchWaitAndRestartRetainExactMembership(t *testing.T) {
	ctx := context.Background()
	m, path := mailbox(t, "local")
	from, to := address("local", "peer"), address("local", "main")
	a := sendMail(t, m, "a", from, to, nil)
	sendMail(t, m, "b", from, to, nil)
	batch, err := m.ClaimMailBatch(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	var cleanup atomic.Int64
	c := conversationFixture(t, filepath.Join(t.TempDir(), "chat.sqlite"), "boot", func(context.Context, core.ModelRequest) (io.ReadCloser, error) { return replySSE("unused"), nil }, &cleanup)
	thread, err := c.Thread(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = m.Session(ctx, a, thread.Config); err != nil {
		t.Fatal(err)
	}
	checkpoint := recoveryCheckpoint()
	checkpoint.LastRequest = batch.ID
	raw, _ := json.Marshal(checkpoint)
	if err = m.SaveSessionContext(ctx, a, raw); err != nil {
		t.Fatal(err)
	}
	if err = m.Wait(ctx, a, "allowance"); err != nil {
		t.Fatal(err)
	}
	sendMail(t, m, "later", from, to, nil)
	m.Close()
	m, err = OpenMailbox(path, "local")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	resumed, err := m.ClaimMailBatch(ctx, a)
	if err != nil || len(resumed.Messages) != 2 {
		t.Fatal("waiting batch absorbed later input", resumed, err)
	}
	if err = m.Finish(ctx, a.ID, true, "done"); err != nil {
		t.Fatal(err)
	}
	next, err := m.Next(ctx, "main")
	if err != nil || next == nil || next.ID != "later" {
		t.Fatal(next, err)
	}
}

func TestResultBatchConcurrentClaimAndRestartFence(t *testing.T) {
	ctx := context.Background()
	m, path := mailbox(t, "local")
	a := enqueueResult(t, m, "A", "main")
	b := protocol.Envelope{Kind: "plugin_event", ID: "plugin-B", From: protocol.AgentRef{NodeID: "local", Kind: "runtime", AgentID: "plugin-example"}, To: address("local", "main"), ConversationID: newConversation(), Plugin: &protocol.PluginNotification{Identity: core.PluginIdentity{ID: "example"}, Event: core.PluginEvent{ID: "B", Title: "B", Body: "body", OccurredAt: "2026-01-01T00:00:00Z"}}}
	if err := storage.Transaction(ctx, m.db, func(tx *sql.Tx) error { return inbox.EnqueueTx(ctx, tx, b, "in", "queued") }); err != nil {
		t.Fatal(err)
	}
	errors := make(chan error, 2)
	for range 2 {
		go func() { errors <- m.ClaimEventBatch(ctx, "main", []string{a.ID, b.ID}) }()
	}
	winners := 0
	for range 2 {
		if err := <-errors; err == nil {
			winners++
		}
	}
	if winners != 1 {
		t.Fatal("duplicate batch execution admitted", winners)
	}
	m.Close()
	var err error
	m, err = OpenMailbox(path, "local")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if err = m.ClaimEventBatch(ctx, "main", []string{a.ID, b.ID}); err == nil {
		t.Fatal("uncertain batch replayed after restart")
	}
	for _, id := range []string{a.ID, b.ID} {
		var state string
		if err = m.db.QueryRow("SELECT state FROM agent_messages WHERE id=?", id).Scan(&state); err != nil || state != "interrupted" {
			t.Fatal("batch member escaped restart fence", state, err)
		}
	}
}

func TestAgentBatchLimitAndReplyCausation(t *testing.T) {
	ctx := context.Background()
	m, _ := mailbox(t, "local")
	from, to := address("local", "peer"), address("local", "main")
	var all []protocol.Envelope
	for i := range inbox.MaxBatchMessages + 1 {
		all = append(all, sendMail(t, m, fmt.Sprintf("peer-%d", i), from, to, nil))
	}
	batch, err := m.ClaimMailBatch(ctx, all[0])
	if err != nil || len(batch.Messages) != inbox.MaxBatchMessages {
		t.Fatal(batch, err)
	}
	reply, err := m.Send(ctx, "reply-auto", to, protocol.SendMail{To: from, Text: "reply to batch"}, &all[0])
	if err != nil || reply.ConversationID != all[0].ConversationID || reply.ReplyTo == nil || *reply.ReplyTo != all[0].ID || reply.Hops != all[0].Hops+1 {
		t.Fatal("batch reply lost automatic parent binding", reply, err)
	}
	if err = m.Finish(ctx, batch.ID, true, "done"); err != nil {
		t.Fatal(err)
	}
	next, err := m.Next(ctx, "main")
	if err != nil || next == nil || next.ID != all[len(all)-1].ID {
		t.Fatal("overflow lost or processed early", next, err)
	}
}

func TestUserArrivalDuringBatchStartsNextTurn(t *testing.T) {
	ctx := context.Background()
	entered, release := make(chan struct{}), make(chan struct{})
	var cleanup, calls atomic.Int64
	c := conversationFixture(t, filepath.Join(t.TempDir(), "chat.sqlite"), "boot", func(ctx context.Context, r core.ModelRequest) (io.ReadCloser, error) {
		if calls.Add(1) == 1 {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return replySSE("handled"), nil
	}, &cleanup)
	for i := range 2 {
		c.mu.Lock()
		_, err := c.queueUserLocked(ctx, protocol.SendMessage{RequestID: fmt.Sprintf("user-%d", i), ConversationID: "main", Text: "original batch"})
		c.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := c.StartPendingUsers(ctx); err != nil {
		t.Fatal(err)
	}
	<-entered
	late, err := c.Send(ctx, protocol.SendMessage{RequestID: "late", ConversationID: "main", Text: "later request"})
	if err != nil {
		t.Fatal(err)
	}
	close(release)
	s := waitSnapshot(t, c, func(s protocol.Snapshot) bool { return s.Turn == nil && len(s.Messages) == 5 })
	if calls.Load() != 2 {
		t.Fatal("later arrival joined in-flight batch or replayed", calls.Load())
	}
	for _, message := range s.Messages {
		if message.ID == late.MessageID && (message.Queued || message.Status != protocol.Complete) {
			t.Fatal(message)
		}
	}
}

func TestNotificationLargeFirstTaskOutcomeDoesNotBlockInbox(t *testing.T) {
	ctx := context.Background()
	m, _ := mailbox(t, "local")
	task := enqueueResult(t, m, "large", "main")
	task.Text = strings.Repeat("\x01", protocol.MaxMailText)
	task.Result.Text = task.Text
	body, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.db.Exec("UPDATE agent_messages SET envelope=? WHERE id=?", string(body), task.ID); err != nil {
		t.Fatal(err)
	}
	later := enqueueResult(t, m, "later", "main")
	events, err := m.Notifications(ctx, "main")
	if err != nil || len(events) != 1 || events[0].ID != task.ID {
		t.Fatal("large first outcome blocked inbox", events, err)
	}
	if err = m.ClaimEventBatch(ctx, "main", []string{task.ID}); err != nil {
		t.Fatal(err)
	}
	if err = m.FinishChat(ctx, "main", "pot-result-"+task.ID, task.ID, true); err != nil {
		t.Fatal(err)
	}
	events, err = m.Notifications(ctx, "main")
	if err != nil || len(events) != 1 || events[0].ID != later.ID {
		t.Fatal("later outcome lost", events, err)
	}
}
