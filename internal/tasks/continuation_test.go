package tasks

import (
	"context"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
	"testing"
)

func completeRun(t *testing.T, s *Service, p *fakePort, task Record) {
	t.Helper()
	if err := s.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	r := p.runs[task.RunID]
	r.Phase, r.Result, r.CleanupConfirmed = Complete, &Terminal{Outcome: core.Success, Text: "done"}, true
	p.runs[task.RunID] = r
	p.mu.Unlock()
	if err := s.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestOwnerBatchArchiveAndContinuationPreserveTaskAndReceipts(t *testing.T) {
	ctx := context.Background()
	s, p, path := fixture(t)
	var records []Record
	var decisions []protocol.TaskDisposition
	for _, id := range []string{"a", "b"} {
		r, err := s.Create(ctx, "assistant", protocol.CreateTask{RequestID: id, Text: "original instructions", Workspace: protocol.TaskWorkspace{Kind: "worktree", ProjectDir: "/project"}})
		if err != nil {
			t.Fatal(err)
		}
		completeRun(t, s, p, r)
		records = append(records, r)
		decisions = append(decisions, protocol.TaskDisposition{TaskID: r.Info.ID, RunID: r.RunID, Archived: true, Reason: "delivered"})
	}
	page, err := s.Page(ctx, "assistant", protocol.TaskQuery{Scope: "attention"})
	if err != nil || page.Total != 2 {
		t.Fatal("completion must retain tasks", page, err)
	}
	if _, err = s.SetDispositions(ctx, "assistant", decisions); err != nil {
		t.Fatal(err)
	}
	page, err = s.Page(ctx, "assistant", protocol.TaskQuery{Scope: "archived"})
	if err != nil || page.Total != 2 {
		t.Fatal(page, err)
	}
	original := records[0]
	user := protocol.AgentRef{NodeID: s.NodeID(), Kind: "user", AgentID: "assistant"}
	// A new input survives restart and immediately removes the archive flag.
	for range 2 {
		if err = s.SendMessage(ctx, "assistant", original.Info.ID, "followup", user, "modify the existing result"); err != nil {
			t.Fatal(err)
		}
	}
	pending, _ := s.Get(ctx, "assistant", original.Info.ID)
	if pending.Info.Archived || pending.Info.Pending != 1 {
		t.Fatal(pending.Info)
	}
	// An invalid second decision rolls back the whole batch.
	unarchive := decisions[1]
	unarchive.Archived = false
	if _, err = s.SetDispositions(ctx, "assistant", []protocol.TaskDisposition{unarchive, decisions[0]}); err == nil {
		t.Fatal("stale batch accepted")
	}
	unchanged, _ := s.Get(ctx, "assistant", records[1].Info.ID)
	if !unchanged.Info.Archived {
		t.Fatal("failed batch partially committed")
	}
	options := s.options
	s.Close()
	restored, err := Open(path, options)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if err = restored.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	current, err := restored.Get(ctx, "assistant", original.Info.ID)
	if err != nil || current.RunID == original.RunID || current.Info.ID != original.Info.ID || current.Info.Archived || current.Generation != original.Generation || current.Request.Workspace != original.Request.Workspace || current.Request.Text != original.Request.Text || current.Launch.Text != "modify the existing result" {
		t.Fatal(current, err)
	}
	old, err := restored.Run(ctx, "assistant", original.Info.ID, original.RunID)
	if err != nil || old.Info.Phase != protocol.TaskComplete || old.Result.Text != "done" {
		t.Fatal("old run overwritten", old, err)
	}
	if _, err = restored.SetDispositions(ctx, "assistant", decisions[:1]); err == nil {
		t.Fatal("old run archived new execution")
	}
	if err = restored.SendMessage(ctx, "assistant", original.Info.ID, "followup", user, "modify the existing result"); err != nil {
		t.Fatal(err)
	}
	if p.launches != 3 {
		t.Fatal("duplicate input launched twice", p.launches)
	}
}

func TestStopEndedTaskDiscardsQueuedFollowupWithoutChangingResult(t *testing.T) {
	for _, mode := range []string{"cancel", "interrupt"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			s, p, _ := fixture(t)
			r, err := s.Create(ctx, "assistant", protocol.CreateTask{RequestID: "original", Text: "work"})
			if err != nil {
				t.Fatal(err)
			}
			completeRun(t, s, p, r)
			if err = s.SendMessage(ctx, "assistant", r.Info.ID, "followup", protocol.AgentRef{NodeID: s.NodeID(), Kind: "user", AgentID: "assistant"}, "additional work"); err != nil {
				t.Fatal(err)
			}
			if err = s.Stop(ctx, "assistant", r.Info.ID, mode); err != nil {
				t.Fatal(err)
			}
			if err = s.Tick(ctx); err != nil {
				t.Fatal(err)
			}
			current, _ := s.Get(ctx, "assistant", r.Info.ID)
			if current.RunID != r.RunID || current.Info.Pending != 0 || current.Info.Phase != protocol.TaskComplete || p.launches != 1 || current.Info.Archived != (mode == "cancel") {
				t.Fatal(current, p.launches)
			}
		})
	}
}

func TestFinalizingGuidanceStartsNextRunButReferencesDoNot(t *testing.T) {
	ctx := context.Background()
	s, p, _ := fixture(t)
	r, err := s.Create(ctx, "assistant", protocol.CreateTask{RequestID: "work", Text: "original"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Steering(ctx, "assistant", r.Info.ID, r.RunID, true); err != nil {
		t.Fatal(err)
	}
	owner := protocol.AgentRef{NodeID: s.NodeID(), Kind: "assistant", AgentID: "assistant"}
	if err = s.SendMessage(ctx, "assistant", r.Info.ID, "late", owner, "new work"); err != nil {
		t.Fatal(err)
	}
	completeRun(t, s, p, r)
	finished, _ := s.Get(ctx, "assistant", r.Info.ID)
	if finished.Info.Pending != 1 || finished.Info.Archived {
		t.Fatal(finished.Info)
	}
	if err = s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	next, _ := s.Get(ctx, "assistant", r.Info.ID)
	if next.RunID == r.RunID || next.RunText != "new work" {
		t.Fatal(next)
	}
	completeRun(t, s, p, *next)
	peer := protocol.AgentRef{NodeID: s.NodeID(), Kind: "assistant", AgentID: "peer"}
	if err = s.SendMessage(ctx, "assistant", r.Info.ID, "reference", peer, "reference"); err != nil {
		t.Fatal(err)
	}
	if err = s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	after, _ := s.Get(ctx, "assistant", r.Info.ID)
	if after.RunID != next.RunID || after.Info.Pending != 0 {
		t.Fatal("reference restarted task", after)
	}
}
