package agenthost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/testutil"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/processscope"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

var fakeBinary string
var fakeAgyBinary string

func TestMain(m *testing.M) {
	if processscope.HandleWorker() {
		return
	}
	if os.Getenv("POT_AGENTHOST_CLI_FIXTURE") == "1" {
		if len(os.Args) > 1 && os.Args[1] == "agent-serve" {
			if err := os.WriteFile(os.Getenv("POT_AGENTHOST_CLI_PID"), []byte(fmt.Sprint(os.Getpid())), 0600); err != nil {
				os.Exit(2)
			}
		}
		adapter := Codex()
		if os.Getenv("POT_AGENTHOST_CLI_ADAPTER") == "antigravity" {
			adapter = Antigravity()
		}
		if err := MainRuntime(adapter, os.Args[1:], os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		os.Exit(0)
	}
	dir, err := os.MkdirTemp("", "pot-codex-agent-fixture-")
	if err != nil {
		panic(err)
	}
	fakeBinary = filepath.Join(dir, "codex")
	cmd := exec.Command("go", "build", "-o", fakeBinary, "./testdata/fake-codex")
	if out, err := cmd.CombinedOutput(); err != nil {
		panic(string(out))
	}
	fakeAgyBinary = filepath.Join(dir, "agy")
	if output, err := exec.Command("go", "build", "-o", fakeAgyBinary, "./testdata/fake-agy").CombinedOutput(); err != nil {
		panic(string(output))
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
func testBackend(t *testing.T) *backend {
	t.Helper()
	root := t.TempDir()
	db, err := storage.OpenDatabase(filepath.Join(root, "tasks.sqlite"), storage.TasksDatabase)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	b, err := newBackend(db, "codex-test", root)
	if err != nil {
		t.Fatal(err)
	}
	b.binary = fakeBinary
	return b
}
func awaitTask(t *testing.T, b *backend, from protocol.AgentRef, id string, want string) agentlink.Task {
	t.Helper()
	deadline := time.Now().Add(testutil.Timeout(t, 15*time.Second))
	for time.Now().Before(deadline) {
		task, err := b.Status(context.Background(), from, id)
		if errors.Is(err, agentlink.ErrNotFound) {
			time.Sleep(20 * time.Millisecond)
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if task.State == want {
			return task
		}
		if terminal(task.State) {
			t.Fatalf("got terminal %+v want %s", task, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("task did not reach " + want)
	return agentlink.Task{}
}
func TestMessageInputsReuseTheirAssistantSession(t *testing.T) {
	b := testBackend(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { b.run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	from := protocol.AgentRef{NodeID: "pot-sender", Kind: "assistant", AgentID: "main"}
	r := agentlink.TaskRequest{RequestID: "first", From: from, Text: "hello"}
	first, err := submitMessageFixture(b, ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	completed := awaitTask(t, b, from, first.ID, "completed")
	if completed.Result != "result: hello\n" {
		t.Fatal(completed)
	}
	original, err := b.resolve(ctx, "codex")
	if err != nil || original.ThreadID == "" {
		t.Fatal(original, err)
	}
	// The next assistant message resumes the same serialized session.
	parent := protocol.Envelope{ID: "incoming-one", Kind: "message", From: from, To: originalIdentity(b, original), ConversationID: "123e4567-e89b-42d3-a456-426614174000", Text: "continue"}
	mailbox := &hostMailbox{db: b.db, local: originalIdentity(b, original), backend: b}
	if _, err = mailbox.Deliver(ctx, parent); err != nil {
		t.Fatal(err)
	}
	if err = b.receiveMessages(ctx); err != nil {
		t.Fatal(err)
	}
	id := jobID(from, "mail:"+parent.ID)
	awaitTask(t, b, from, id, "completed")
	if err = b.publishMessages(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := b.resolve(ctx, "codex")
	if err != nil || after.ThreadID != original.ThreadID {
		t.Fatal("session replaced", after, err)
	}
	outgoing, err := mailbox.PendingNode(ctx, parent.To.NodeID, from.NodeID)
	if err != nil || len(outgoing) != 1 {
		t.Fatal(outgoing, err)
	}
	reply := outgoing[0]
	if reply.ConversationID != parent.ConversationID || reply.ReplyTo == nil || *reply.ReplyTo != parent.ID || reply.Hops != 1 || reply.To != from {
		t.Fatal("reply lost original route", reply)
	}
	if err = b.publishMessages(ctx); err != nil {
		t.Fatal(err)
	}
	outgoing, _ = mailbox.PendingNode(ctx, parent.To.NodeID, from.NodeID)
	if len(outgoing) != 1 {
		t.Fatal("duplicate reply", outgoing)
	}
}
func originalIdentity(b *backend, a assistant) protocol.AgentRef { return b.identity(a).Address }

func TestMessageReplyAfterLargeToolResult(t *testing.T) {
	b := testBackend(t)
	ctx := context.Background()
	a, err := b.resolve(ctx, "codex")
	if err != nil {
		t.Fatal(err)
	}
	parent := protocol.Envelope{ID: "large-tool-message", Kind: "message", From: protocol.AgentRef{NodeID: "pot", Kind: "assistant", AgentID: "main"}, To: b.identity(a).Address, ConversationID: "123e4567-e89b-42d3-a456-426614174000", Text: "large-tool-result"}
	mailbox := &hostMailbox{db: b.db, local: parent.To, backend: b}
	if _, err = mailbox.Deliver(ctx, parent); err != nil {
		t.Fatal(err)
	}
	if err = b.receiveMessages(ctx); err != nil {
		t.Fatal(err)
	}
	j, err := b.read(ctx, parent.From, jobID(parent.From, "mail:"+parent.ID))
	if err != nil {
		t.Fatal(err)
	}
	b.execute(ctx, j)
	completed := awaitTask(t, b, parent.From, j.Task.ID, "completed")
	if completed.Result != "result: large-tool-result\n" {
		t.Fatalf("final reply contains progress or lost its answer: %+v", completed)
	}
	if err = b.publishMessages(ctx); err != nil {
		t.Fatal(err)
	}
	replies, err := mailbox.PendingNode(ctx, parent.To.NodeID, parent.From.NodeID)
	if err != nil || len(replies) != 1 {
		t.Fatal(replies, err)
	}
	if reply := replies[0]; reply.Text != completed.Result || reply.To != parent.From || reply.ReplyTo == nil || *reply.ReplyTo != parent.ID || reply.ConversationID != parent.ConversationID {
		t.Fatal("final answer lost its original route", reply)
	}
}

func TestMessageReplyWaitsForTurnCompletion(t *testing.T) {
	b := testBackend(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a, err := b.resolve(ctx, "codex")
	if err != nil {
		t.Fatal(err)
	}
	parent := protocol.Envelope{ID: "waiting-message", Kind: "message", From: protocol.AgentRef{NodeID: "pot", Kind: "assistant", AgentID: "main"}, To: b.identity(a).Address, ConversationID: "123e4567-e89b-42d3-a456-426614174000", Text: "hold-after-answer"}
	mailbox := &hostMailbox{db: b.db, local: parent.To, backend: b}
	if _, err = mailbox.Deliver(ctx, parent); err != nil {
		t.Fatal(err)
	}
	if err = b.receiveMessages(ctx); err != nil {
		t.Fatal(err)
	}
	j, err := b.read(ctx, parent.From, jobID(parent.From, "mail:"+parent.ID))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); b.execute(ctx, j) }()
	t.Cleanup(func() { cancel(); <-done })
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err = os.Stat(filepath.Join(a.Workspace, "answer-ready")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fixture did not produce the final answer", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	// The fixture emitted its final answer but has not ended the turn.
	if err = b.publishMessages(ctx); err != nil {
		t.Fatal(err)
	}
	replies, err := mailbox.PendingNode(ctx, parent.To.NodeID, parent.From.NodeID)
	if err != nil || len(replies) != 0 {
		t.Fatal("answer delivered before turn completion", replies, err)
	}
	if err = os.WriteFile(filepath.Join(a.Workspace, "complete-turn"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	awaitTask(t, b, parent.From, j.Task.ID, "completed")
	for i := 0; i < 2; i++ {
		if err = b.publishMessages(ctx); err != nil {
			t.Fatal(err)
		}
	}
	replies, err = mailbox.PendingNode(ctx, parent.To.NodeID, parent.From.NodeID)
	if err != nil || len(replies) != 1 || replies[0].Text != "result: hold-after-answer\n" {
		t.Fatal("turn did not deliver one final reply", replies, err)
	}
}

func TestUnsuccessfulMessageRepliesIncludeOutcome(t *testing.T) {
	for _, state := range []string{"failed", "cancelled"} {
		for _, tc := range []struct{ name, output, want string }{
			{"empty", "", ""},
			{"partial", "partial output", "partial output"},
			{"escaped", strings.Repeat("\x00", maxResult), strings.Repeat("\x00", maxResult/2)},
		} {
			t.Run(state+"/"+tc.name, func(t *testing.T) {
				b := testBackend(t)
				ctx := context.Background()
				a, err := b.resolve(ctx, "codex")
				if err != nil {
					t.Fatal(err)
				}
				parent := protocol.Envelope{ID: "failed-message", Kind: "message", From: protocol.AgentRef{NodeID: "pot", Kind: "assistant", AgentID: "main"}, To: b.identity(a).Address, ConversationID: "123e4567-e89b-42d3-a456-426614174000", Text: "request"}
				mailbox := &hostMailbox{db: b.db, local: parent.To, backend: b}
				if _, err = mailbox.Deliver(ctx, parent); err != nil {
					t.Fatal(err)
				}
				if err = b.receiveMessages(ctx); err != nil {
					t.Fatal(err)
				}
				if err = b.update(ctx, jobID(parent.From, "mail:"+parent.ID), func(j *job) error {
					j.Task.State, j.Task.Result, j.Task.Error = state, tc.output, "codex_jsonl_read_failed_outcome_unknown"
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				for i := 0; i < 2; i++ {
					if err = b.publishMessages(ctx); err != nil {
						t.Fatal(err)
					}
				}
				replies, err := mailbox.PendingNode(ctx, parent.To.NodeID, parent.From.NodeID)
				if err != nil || len(replies) != 1 {
					t.Fatal("missing or repeated terminal reply", replies, err)
				}
				var outcome map[string]string
				if err = json.Unmarshal([]byte(replies[0].Text), &outcome); err != nil || outcome["state"] != state || outcome["error"] != "codex_jsonl_read_failed_outcome_unknown" || outcome["partial_output"] != tc.want {
					t.Fatal("failure concealed by partial text", replies[0], err)
				}

			})
		}
	}
}
func TestMessageRetriesRetainExplicitRecipientAndCallerScope(t *testing.T) {
	b := testBackend(t)
	ctx := context.Background()
	from := protocol.AgentRef{NodeID: "pot-sender", Kind: "assistant", AgentID: "main"}
	r := agentlink.TaskRequest{RequestID: "same", From: from, Target: agentlink.AssistantTarget(b.nodeID, "codex"), Text: "hello"}
	first, err := submitMessageFixture(b, ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if err = b.ownerConfigure(ctx, assistant{ID: "second", Name: "Second", Workspace: t.TempDir(), Mode: "read-only", Default: true}); err != nil {
		t.Fatal(err)
	}
	retry, err := submitMessageFixture(b, ctx, r)
	if err != nil || retry.ID != first.ID || retry.Receiver.Address.AgentID != "codex" {
		t.Fatal("retry moved to changed default", retry, err)
	}
	r.RequestID, r.Target.AgentID = "new", "second"
	second, err := submitMessageFixture(b, ctx, r)
	if err != nil || second.Receiver.Address.AgentID != "second" {
		t.Fatal(second, err)
	}
	r.RequestID, r.Target.AgentID = "same", "codex"
	r.Text = "changed"
	if _, err = submitMessageFixture(b, ctx, r); !errors.Is(err, agentlink.ErrConflict) {
		t.Fatal("changed retry accepted", err)
	}
	other := from
	other.AgentID = "different"
	if _, err = b.Status(ctx, other, first.ID); !errors.Is(err, agentlink.ErrNotFound) {
		t.Fatal("foreign task visible", err)
	}
}
func TestQueuedCancelAndRestartFence(t *testing.T) {
	b := testBackend(t)
	ctx := context.Background()
	from := protocol.AgentRef{NodeID: "pot-sender", Kind: "assistant", AgentID: "main"}
	first, err := submitMessageFixture(b, ctx, agentlink.TaskRequest{RequestID: "cancel", From: from, Text: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	cancelled, err := b.Cancel(ctx, from, first.ID)
	if err != nil || cancelled.State != "cancelled" {
		t.Fatal(cancelled, err)
	}
	second, err := submitMessageFixture(b, ctx, agentlink.TaskRequest{RequestID: "running", From: from, Text: "hang"})
	if err != nil {
		t.Fatal(err)
	}
	if err = b.update(ctx, second.ID, func(j *job) error { j.Task.State = "running"; return nil }); err != nil {
		t.Fatal(err)
	}
	if err = b.recover(ctx); err != nil {
		t.Fatal(err)
	}
	recovered, err := b.Status(ctx, from, second.ID)
	if err != nil || recovered.State != "failed" || recovered.Error != "codex_host_restarted_outcome_unknown" {
		t.Fatal(recovered, err)
	}
	if _, err = submitMessageFixture(b, ctx, agentlink.TaskRequest{RequestID: "unsafe-replay", From: from, Text: "hello"}); err == nil {
		t.Fatal("uncertain cleanup failed to fence new admission")
	}
}
func TestRunningCancelWaitsForInterruptedTurn(t *testing.T) {
	b := testBackend(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { b.run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	from := protocol.AgentRef{NodeID: "pot-sender", Kind: "assistant", AgentID: "main"}
	task, err := submitMessageFixture(b, ctx, agentlink.TaskRequest{RequestID: "hang", From: from, Text: "hang"})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		j, err := b.read(ctx, from, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if j.TurnID != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	requested, err := b.Cancel(ctx, from, task.ID)
	if err != nil || requested.State != "cancelling" {
		t.Fatal(requested, err)
	}
	awaitTask(t, b, from, task.ID, "cancelled")
}

func TestAssistantsParallelButOwnSessionSerial(t *testing.T) {
	b := testBackend(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { b.run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	if err := b.ownerConfigure(ctx, assistant{ID: "second", Name: "Second", Workspace: t.TempDir(), Mode: "read-only"}); err != nil {
		t.Fatal(err)
	}
	from := protocol.AgentRef{NodeID: "pot-sender", Kind: "assistant", AgentID: "main"}
	first, err := submitMessageFixture(b, ctx, agentlink.TaskRequest{RequestID: "hang-first", From: from, Target: agentlink.AssistantTarget(b.nodeID, "codex"), Text: "hang"})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		j, err := b.read(ctx, from, first.ID)
		if err != nil {
			t.Fatal(err)
		}
		if j.TurnID != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	next, err := submitMessageFixture(b, ctx, agentlink.TaskRequest{RequestID: "queued-next", From: from, Target: agentlink.AssistantTarget(b.nodeID, "codex"), Text: "after interruption"})
	if err != nil {
		t.Fatal(err)
	}
	parallel, err := submitMessageFixture(b, ctx, agentlink.TaskRequest{RequestID: "other-session", From: from, Target: agentlink.AssistantTarget(b.nodeID, "second"), Text: "parallel"})
	if err != nil {
		t.Fatal(err)
	}
	awaitTask(t, b, from, parallel.ID, "completed")
	waiting, err := b.Status(ctx, from, next.ID)
	if err != nil || waiting.State != "queued" {
		t.Fatal("same session executed concurrently", waiting, err)
	}
	if _, err = b.Cancel(ctx, from, first.ID); err != nil {
		t.Fatal(err)
	}
	awaitTask(t, b, from, first.ID, "cancelled")
	awaitTask(t, b, from, next.ID, "completed")
	a, err := b.resolve(ctx, "codex")
	if err != nil {
		t.Fatal(err)
	}
	other, err := b.resolve(ctx, "second")
	if err != nil || a.ThreadID == other.ThreadID {
		t.Fatal("assistants share conversation identity", a, other, err)
	}
}
func TestVirtualAssistantSendKeepsActiveMessageParent(t *testing.T) {
	b := testBackend(t)
	ctx := context.Background()
	a, err := b.resolve(ctx, "codex")
	if err != nil {
		t.Fatal(err)
	}
	from := protocol.AgentRef{NodeID: "peer-node", Kind: "assistant", AgentID: "remote"}
	parent := protocol.Envelope{ID: "parent-message", Kind: "message", From: from, To: b.identity(a).Address, ConversationID: "123e4567-e89b-42d3-a456-426614174000", Text: "reference", Hops: 7}
	task, err := b.submitMessage(ctx, agentlink.TaskRequest{RequestID: "message-parent", From: from, Target: agentlink.AssistantTarget(b.nodeID, a.ID), Text: parent.Text}, parent)
	if err != nil {
		t.Fatal(err)
	}
	if err = b.update(ctx, task.ID, func(j *job) error { j.Task.State = "running"; return nil }); err != nil {
		t.Fatal(err)
	}
	mailbox := &hostMailbox{db: b.db, local: parent.To, backend: b}
	reply, err := mailbox.sendFrom(ctx, parent.To, from, "virtual-send", "response", task.ID)
	if err != nil || reply.Hops != 8 || reply.ReplyTo == nil || *reply.ReplyTo != parent.ID || reply.ConversationID != parent.ConversationID {
		t.Fatal(reply, err)
	}
	if err = b.update(ctx, task.ID, func(j *job) error { j.Message.Hops = 130; return nil }); err != nil {
		t.Fatal(err)
	}
	if reply, err = mailbox.sendFrom(ctx, parent.To, from, "continue", "response", task.ID); err != nil || reply.Hops != 131 || reply.ReplyTo == nil || *reply.ReplyTo != parent.ID {
		t.Fatal("long virtual session lost continuation or parent", reply, err)
	}
	if terminal := messageReply(job{Message: &protocol.Envelope{ID: parent.ID, From: from, ConversationID: parent.ConversationID, Hops: 130}, Task: agentlink.Task{ID: task.ID, State: "completed", Result: "done"}}); terminal == nil || terminal.Hops != 131 {
		t.Fatal("long correspondence suppressed terminal reply", terminal)
	}
}

func TestUnknownCleanupFencesAlreadyQueuedWork(t *testing.T) {
	b := testBackend(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	from := protocol.AgentRef{NodeID: "pot-sender", Kind: "assistant", AgentID: "main"}
	first, err := submitMessageFixture(b, ctx, agentlink.TaskRequest{RequestID: "uncertain", From: from, Text: "first"})
	if err != nil {
		t.Fatal(err)
	}
	queued, err := submitMessageFixture(b, ctx, agentlink.TaskRequest{RequestID: "already-queued", From: from, Text: "second"})
	if err != nil {
		t.Fatal(err)
	}
	if err = b.update(ctx, first.ID, func(j *job) error { j.Task.State = "failed"; j.CleanupUncertain = true; return nil }); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { b.run(ctx); close(done) }()
	time.Sleep(250 * time.Millisecond)
	cancel()
	<-done
	got, err := b.Status(context.Background(), from, queued.ID)
	if err != nil || got.State != "queued" {
		t.Fatal("queued work escaped cleanup fence", got, err)
	}
}
