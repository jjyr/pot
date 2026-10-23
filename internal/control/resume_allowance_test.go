package control

import (
	"context"
	"testing"

	"github.com/jjyr/pot/internal/usage"
)

func TestResumeAllowanceStaleTurnAndExactRetryCannotReleaseLaterMainWait(t *testing.T) {
	t.Parallel()
	s, _ := mainRecoveryFixture(t, false, true, false, "previous-boot")
	ctx := context.Background()
	s.Usage.Wait(ctx, usage.WaitRequest{Scope: "main", Turn: "a", AtMS: 1, Required: 1})
	a, _ := s.Usage.Waiting(ctx, "main")
	if _, err := s.Usage.AdmitWaitRelease(ctx, "main", "original-resume", *a); err != nil {
		t.Fatal(err)
	}
	if err := s.Usage.CompleteWaitRelease(ctx, "main", "original-resume"); err != nil {
		t.Fatal(err)
	}
	s.Usage.Wait(ctx, usage.WaitRequest{Scope: "main", Turn: "b", AtMS: 2, Required: 1})
	if err := s.ResumeAllowance(ctx, "main", ResumeAllowanceCommand{RequestID: "stale-new-request", Turn: "a", Since: 1}); err == nil {
		t.Fatal("stale displayed turn released current wait")
	}
	if err := s.ResumeAllowance(ctx, "main", ResumeAllowanceCommand{RequestID: "original-resume", Turn: "a", Since: 1}); err != nil {
		t.Fatal("original receipt retry failed", err)
	}
	b, _ := s.Usage.Waiting(ctx, "main")
	if b.Turn != "b" || b.Released {
		t.Fatal("old action affected new turn", b)
	}
	if err := s.ResumeAllowance(ctx, "main", ResumeAllowanceCommand{RequestID: "original-resume", Turn: "b", Since: 2}); err == nil {
		t.Fatal("request ID rebound to new wait")
	}
}
