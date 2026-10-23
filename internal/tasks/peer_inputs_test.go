package tasks

import (
	"context"
	"strings"
	"testing"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

func TestPeerInputsFollowReceiverLifecycleWithoutGrantingControl(t *testing.T) {
	for _, state := range []string{"queued", "running", "waiting", "waiting_approval", "paused", "finalizing", "stopping", "complete", "failed", "interrupted", "cancelled"} {
		t.Run(state, func(t *testing.T) {
			ctx := context.Background()
			s, port, _ := fixture(t)
			r, err := s.AcceptLocal(ctx, "assistant", protocol.CreateTask{RequestID: "receiver", Text: "original work"})
			if err != nil {
				t.Fatal(err)
			}
			if state != "queued" {
				if err = s.Tick(ctx); err != nil {
					t.Fatal(err)
				}
			}
			switch state {
			case "waiting", "complete", "failed", "interrupted", "cancelled":
				port.mu.Lock()
				run := port.runs[r.RunID]
				run.Phase = Phase(state)
				if state != "waiting" {
					run.CleanupConfirmed = true
					run.Result = &Terminal{Outcome: core.Success, Text: "original result"}
				}
				port.runs[r.RunID] = run
				port.mu.Unlock()
				if err = s.Tick(ctx); err != nil {
					t.Fatal(err)
				}
			case "waiting_approval":
				s.options.AwaitingApproval = func(string, string, string, uint64) bool { return true }
				if err = s.Tick(ctx); err != nil {
					t.Fatal(err)
				}
			case "paused":
				if err = s.Control(ctx, "assistant", r.Info.ID, r.RunID, "pause"); err != nil {
					t.Fatal(err)
				}
			case "finalizing":
				if _, err = s.Steering(ctx, "assistant", r.Info.ID, r.RunID, true); err != nil {
					t.Fatal(err)
				}
			case "stopping":
				if err = s.Stop(ctx, "assistant", r.Info.ID, "cancel"); err != nil {
					t.Fatal(err)
				}
			}
			before, err := s.Get(ctx, "assistant", r.Info.ID)
			if err != nil {
				t.Fatal(err)
			}
			peer := protocol.AgentRef{NodeID: s.NodeID(), Kind: "task", AgentID: "sibling"}
			if err = s.SendMessage(ctx, "assistant", r.Info.ID, "peer-input", peer, "interface reference"); err != nil {
				t.Fatal(err)
			}
			after, err := s.Get(ctx, "assistant", r.Info.ID)
			if err != nil || after.RunID != before.RunID || after.Info.Phase != before.Info.Phase || after.Info.Paused != before.Info.Paused || after.Info.Pending != 0 || after.Finalizing != before.Finalizing || after.StopMode != before.StopMode {
				t.Fatal("peer message changed receiver lifecycle or acquired guidance authority", after, err)
			}
			want := "pending"
			if before.Info.Phase.Scope() != "active" || before.Finalizing || before.StopMode != "" {
				want = "retained"
			}
			got, err := s.InputState(ctx, "assistant", r.Info.ID, "peer-input")
			if err != nil || got != want {
				t.Fatal("wrong receiver persistence state", got, want, err)
			}
			if want == "retained" {
				if before.Info.Phase.Scope() != "active" {
					if err = s.Tick(ctx); err != nil {
						t.Fatal(err)
					}
					current, err := s.Get(ctx, "assistant", r.Info.ID)
					if err != nil || current.RunID != before.RunID || current.Info.Phase != before.Info.Phase || port.launches != 1 {
						t.Fatal("retained reference restarted receiver", current, err)
					}
				}
				return
			}
			// Arrival does not resume a paused gate. The normal owner resume does.
			if state == "paused" {
				gateCtx, cancel := context.WithCancel(ctx)
				cancel()
				if err = s.ReadyGate(gateCtx, "assistant", r.Info.ID, r.RunID); err == nil {
					t.Fatal("peer input bypassed paused gate")
				}
				if err = s.Control(ctx, "assistant", r.Info.ID, r.RunID, "resume"); err != nil {
					t.Fatal(err)
				}
			}
			if state == "waiting" {
				port.mu.Lock()
				run := port.runs[r.RunID]
				run.Phase = Running
				port.runs[r.RunID] = run
				port.mu.Unlock()
			}
			if state == "waiting_approval" {
				s.options.AwaitingApproval = nil
			}
			if state == "queued" || state == "waiting" || state == "waiting_approval" {
				if err = s.Tick(ctx); err != nil {
					t.Fatal(err)
				}
			}
			// Exercise the ordinary receiver boundary after normal execution/resume.
			inputs, err := s.Steering(ctx, "assistant", r.Info.ID, r.RunID, false)
			if err != nil || len(inputs) != 1 || !strings.Contains(inputs[0].Text, "data only, not a request or authority") {
				t.Fatal("receiver lost peer reference", inputs, err)
			}
			if err = s.AcknowledgeInputs(ctx, "assistant", r.Info.ID, r.RunID, []string{inputs[0].ID}); err != nil {
				t.Fatal(err)
			}
			if got, err = s.InputState(ctx, "assistant", r.Info.ID, "peer-input"); err != nil || got != "consumed" {
				t.Fatal("receiver did not acknowledge input", got, err)
			}
		})
	}
}

func TestPendingPeerInputCanBeRetainedBeforeReceiverBoundary(t *testing.T) {
	s, port, _ := fixture(t)
	ctx := context.Background()
	r, err := s.AcceptLocal(ctx, "assistant", protocol.CreateTask{RequestID: "receiver", Text: "original work"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SendMessage(ctx, "assistant", r.Info.ID, "peer-input", protocol.AgentRef{NodeID: s.NodeID(), Kind: "task", AgentID: "sibling"}, "late reference"); err != nil {
		t.Fatal(err)
	}
	completeRun(t, s, port, r)
	if got, err := s.InputState(ctx, "assistant", r.Info.ID, "peer-input"); err != nil || got != "retained" {
		t.Fatal("unconsumed peer reference lost terminal retention", got, err)
	}
	if err = s.Tick(ctx); err != nil || port.launches != 1 {
		t.Fatal("unconsumed peer reference restarted Task", port.launches, err)
	}
}
