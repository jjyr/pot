package agenthost

import (
	"context"
	"errors"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/protocol"
	"os"
	"testing"
)

func TestAssistantRetirementPreservesHistoryAndCannotResurrect(t *testing.T) {
	b := testBackend(t)
	ctx := context.Background()
	a, err := b.resolve(ctx, "codex")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.db.Exec("UPDATE agent_adapter_assistants SET thread_id='original-thread' WHERE id='codex'"); err != nil {
		t.Fatal(err)
	}
	if err = b.ownerConfigure(ctx, assistant{ID: "other", Workspace: t.TempDir(), Mode: "read-only"}); err != nil {
		t.Fatal(err)
	}
	r := agentlink.TaskRequest{From: protocol.AgentRef{NodeID: "peer", Kind: "assistant", AgentID: "main"}, RequestID: "history", Target: agentlink.AssistantTarget(b.nodeID, a.ID), Text: "task"}
	j, err := submitMessageFixture(b, ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	request := agentlink.DeleteAssistantRequest{AssistantID: a.ID, Revision: a.Revision}
	if err = b.DeleteAssistant(ctx, request); !errors.Is(err, agentlink.ErrAssistantBusy) {
		t.Fatal("queued work allowed retirement", err)
	}
	if err = b.update(ctx, j.ID, func(j *job) error { j.Task.State = "completed"; j.Task.Result = "saved result"; return nil }); err != nil {
		t.Fatal(err)
	}
	var before string
	b.db.QueryRow("SELECT data FROM agent_adapter_jobs WHERE id=?", j.ID).Scan(&before)
	stale := request
	stale.Revision++
	if err = b.DeleteAssistant(ctx, stale); !errors.Is(err, agentlink.ErrAssistantChanged) {
		t.Fatal("stale revision", err)
	}
	for range 2 {
		if err = b.DeleteAssistant(ctx, request); err != nil {
			t.Fatal("retirement/retry", err)
		}
	}
	d, err := b.Directory(ctx)
	if err != nil || len(d.Assistants()) != 1 || d.Assistants()[0].Address.AgentID != "other" || d.DefaultAssistant() != "" {
		t.Fatal("directory/default", d, err)
	}
	if _, err = b.resolve(ctx, a.ID); !errors.Is(err, agentlink.ErrNotFound) {
		t.Fatal("retired identity resolved", err)
	}
	if err = b.ownerConfigure(ctx, a); !errors.Is(err, agentlink.ErrNotFound) {
		t.Fatal("owner configure resurrected identity", err)
	}
	if _, err = b.Profile(ctx, agentlink.ProfileRequest{AssistantID: a.ID, Action: "get"}); !errors.Is(err, agentlink.ErrNotFound) {
		t.Fatal("retired profile reopened", err)
	}
	if _, err = submitMessageFixture(b, ctx, r); err != nil {
		t.Fatal("original receipt lost", err)
	}
	r.RequestID = "new"
	if _, err = submitMessageFixture(b, ctx, r); !errors.Is(err, agentlink.ErrNotFound) {
		t.Fatal("new message admitted", err)
	}
	var after, thread string
	b.db.QueryRow("SELECT data FROM agent_adapter_jobs WHERE id=?", j.ID).Scan(&after)
	b.db.QueryRow("SELECT thread_id FROM agent_adapter_assistants WHERE id=?", a.ID).Scan(&thread)
	if before != after || thread != "original-thread" {
		t.Fatal("retirement changed accepted history")
	}
	if _, err = os.Stat(a.Workspace); err != nil {
		t.Fatal("workspace removed", err)
	}
	if _, err = newBackend(b.db, b.nodeID, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if _, err = b.resolve(ctx, a.ID); !errors.Is(err, agentlink.ErrNotFound) {
		t.Fatal("restart resurrected identity", err)
	}
}

func TestAssistantRetirementRejectsActiveAndUncertainWork(t *testing.T) {
	for _, state := range []string{"running", "cancelling", "waiting_approval", "unknown", "failed"} {
		t.Run(state, func(t *testing.T) {
			b := testBackend(t)
			ctx := context.Background()
			a, _ := b.resolve(ctx, "codex")
			j, err := submitMessageFixture(b, ctx, agentlink.TaskRequest{From: protocol.AgentRef{NodeID: "peer", Kind: "assistant", AgentID: "main"}, RequestID: "busy", Text: "task"})
			if err != nil {
				t.Fatal(err)
			}
			if err = b.update(ctx, j.ID, func(j *job) error { j.Task.State = state; j.CleanupUncertain = state == "failed"; return nil }); err != nil {
				t.Fatal(err)
			}
			if err = b.DeleteAssistant(ctx, agentlink.DeleteAssistantRequest{AssistantID: a.ID, Revision: a.Revision}); !errors.Is(err, agentlink.ErrAssistantBusy) {
				t.Fatal(err)
			}
		})
	}
}

func TestAssistantRetirementBlocksPendingMailAndRetainsReceipts(t *testing.T) {
	b := testBackend(t)
	ctx := context.Background()
	a, _ := b.resolve(ctx, "codex")
	m := hostMailbox{db: b.db, backend: b, local: protocol.AgentRef{NodeID: b.nodeID, Kind: "assistant", AgentID: a.ID}}
	e := protocol.Envelope{ID: "mail", Kind: "message", From: protocol.AgentRef{NodeID: "peer", Kind: "assistant", AgentID: "main"}, To: m.local, ConversationID: "00000000-0000-4000-8000-000000000001", Text: "pending reference"}
	if _, err := m.Deliver(ctx, e); err != nil {
		t.Fatal(err)
	}
	r := agentlink.DeleteAssistantRequest{AssistantID: a.ID, Revision: a.Revision}
	if err := b.DeleteAssistant(ctx, r); !errors.Is(err, agentlink.ErrAssistantBusy) {
		t.Fatal("pending message lost", err)
	}
	if _, err := b.db.Exec("UPDATE agent_messages SET state='processed' WHERE id='mail'"); err != nil {
		t.Fatal(err)
	}
	if err := b.DeleteAssistant(ctx, r); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Deliver(ctx, e); err != nil {
		t.Fatal("accepted receipt lost", err)
	}
	e.ID = "new-mail"
	if _, err := m.Deliver(ctx, e); !errors.Is(err, agentlink.ErrNotFound) {
		t.Fatal("new delivery reached retired identity", err)
	}
}

func TestAssistantRetirementRacesNewMessageAdmission(t *testing.T) {
	for range 6 {
		b := testBackend(t)
		ctx := context.Background()
		a, _ := b.resolve(ctx, "codex")
		start := make(chan struct{})
		deleted := make(chan error, 1)
		submitted := make(chan error, 1)
		go func() {
			<-start
			deleted <- b.DeleteAssistant(ctx, agentlink.DeleteAssistantRequest{AssistantID: a.ID, Revision: a.Revision})
		}()
		go func() {
			<-start
			_, err := submitMessageFixture(b, ctx, agentlink.TaskRequest{From: protocol.AgentRef{NodeID: "peer", Kind: "assistant", AgentID: "main"}, RequestID: "racing", Text: "new message"})
			submitted <- err
		}()
		close(start)
		removeErr, submitErr := <-deleted, <-submitted
		if removeErr == nil {
			if !errors.Is(submitErr, agentlink.ErrNotFound) {
				t.Fatal("new work admitted after retirement", submitErr)
			}
		} else if !errors.Is(removeErr, agentlink.ErrAssistantBusy) || submitErr != nil {
			t.Fatal(removeErr, submitErr)
		}
	}
}
