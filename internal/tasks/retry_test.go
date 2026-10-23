package tasks

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

func failRetryTask(t *testing.T, s *Service, p *fakePort, record Record) {
	t.Helper()
	ctx := context.Background()
	if err := s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	run := p.runs[record.RunID]
	run.Phase = Failed
	run.CleanupConfirmed = true
	run.Result = &Terminal{Outcome: core.Outcome{Kind: "failed", Error: "temporary upstream failure"}}
	p.runs[record.RunID] = run
	p.mu.Unlock()
	if err := s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestRetryPreservesScopeAndHasOneDurableReplacementPerSource(t *testing.T) {
	ctx := context.Background()
	s, p, path := fixture(t)
	skills := []string{"research"}
	original, err := s.AcceptLocal(ctx, "assistant", protocol.CreateTask{RequestID: "original", Text: "Research\nKeep the original constraints.", Skills: &skills, Requirements: protocol.TaskRequirements{Tools: true}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SendMessage(ctx, "assistant", original.Info.ID, "guidance", protocol.AgentRef{NodeID: "machine", Kind: "assistant", AgentID: "assistant"}, "Preserve this additional constraint."); err != nil {
		t.Fatal(err)
	}
	if err := s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.AcknowledgeInputs(ctx, "assistant", original.Info.ID, original.RunID, []string{"guidance"}); err != nil {
		t.Fatal(err)
	}
	failRetryTask(t, s, p, original)
	retry, err := s.Retry(ctx, "assistant", original.Info.ID, "Check upstream availability before continuing.")
	if err != nil {
		t.Fatal(err)
	}
	if retry.Info.ID == original.Info.ID || retry.NodeID != original.NodeID || !reflect.DeepEqual(retry.Config, original.Config) || !reflect.DeepEqual(retry.Request.Skills, original.Request.Skills) || retry.Request.Requirements != original.Request.Requirements || !strings.Contains(retry.Request.Text, original.Request.Text) || !strings.Contains(retry.Request.Text, "Preserve this additional constraint.") {
		t.Fatal("replacement lost original scope", retry)
	}
	// Reopening storage retains the original receipt without consuming recovery
	// opportunities for later failed replacements.
	reopened, err := Open(filepath.Clean(path), s.options)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	again, err := reopened.Retry(ctx, "assistant", original.Info.ID, "Do not create another replacement.")
	if err != nil || again.Info.ID != retry.Info.ID {
		t.Fatal(again, err)
	}
	for range 3 {
		failRetryTask(t, reopened, p, retry)
		if allowed, err := reopened.CanRetry(ctx, "assistant", retry.Info.ID); err != nil || !allowed {
			t.Fatal("failed replacement lost recovery eligibility", allowed, err)
		}
		next, err := reopened.Retry(ctx, "assistant", retry.Info.ID, "Diagnose the next transient failure.")
		if err != nil || next.Info.ID == retry.Info.ID || next.NodeID != original.NodeID || !reflect.DeepEqual(next.Config, original.Config) {
			t.Fatal("recovery chain stopped or changed scope", next, err)
		}
		again, err := reopened.Retry(ctx, "assistant", retry.Info.ID, "Repeat the same receipt.")
		if err != nil || again.Info.ID != next.Info.ID {
			t.Fatal("duplicate recovery created another replacement", again, err)
		}
		retry = next
	}
	if _, err := reopened.Retry(ctx, "other", original.Info.ID, "Cross-owner retry."); err == nil {
		t.Fatal("cross-owner retry accepted")
	}
}

func TestRetryRejectsActiveAndStoppedTasks(t *testing.T) {
	s, _, _ := fixture(t)
	ctx := context.Background()
	r, err := s.AcceptLocal(ctx, "assistant", protocol.CreateTask{RequestID: "active", Text: "Work\nDo it."})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Retry(ctx, "assistant", r.Info.ID, "Retry active work."); err == nil {
		t.Fatal("active work duplicated")
	}
	if err := s.Stop(ctx, "assistant", r.Info.ID, "cancel"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Retry(ctx, "assistant", r.Info.ID, "Ignore cancellation."); err == nil {
		t.Fatal("cancelled work restarted")
	}
}
