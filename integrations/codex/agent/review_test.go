package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/protocol"
)

func reviewQuery(from protocol.AgentRef) agentlink.ReviewRequest {
	return agentlink.ReviewRequest{From: protocol.AgentRef{NodeID: from.NodeID, Kind: "user", AgentID: "operator"}, Caller: from, Action: "read"}
}

func awaitReview(t *testing.T, b *backend, r agentlink.ReviewRequest) agentlink.ReviewState {
	t.Helper()
	end := time.Now().Add(10 * time.Second)
	for time.Now().Before(end) {
		value, err := b.Review(context.Background(), r)
		if err != nil {
			t.Fatal(err)
		}
		if len(value.Pending) > 0 {
			current, err := b.read(context.Background(), r.Caller, value.Pending[0].JobID)
			if err == nil && current.Task.State == "waiting_approval" {
				return value
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no pending review")
	return agentlink.ReviewState{}
}

func TestHumanApprovalResumesOnlyOriginalOperation(t *testing.T) {
	for _, action := range []string{"allow_once", "deny", "cancel"} {
		t.Run(action, func(t *testing.T) {
			b := testBackend(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			from := protocol.AgentRef{NodeID: "pot", Kind: "assistant", AgentID: "main"}
			accepted, err := b.Submit(ctx, agentlink.TaskRequest{From: from, Target: agentlink.NodeTarget(b.nodeID), RequestID: "request", Text: "approve"})
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan struct{})
			go func() { b.run(ctx); close(done) }()
			defer func() { cancel(); <-done }()
			query := reviewQuery(from)
			pending := awaitReview(t, b, query)
			a, _ := b.resolve(ctx, "codex")
			marker := filepath.Join(a.Workspace, "approval-executed")
			if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("operation ran before approval", err)
			}
			var args map[string]any
			if json.Unmarshal(pending.Pending[0].Params, &args) != nil || args["command"] != "untrusted" {
				t.Fatal(pending)
			}
			decision := query
			decision.Action = "allow_once"
			decision.ID = pending.Pending[0].ID
			wrong := decision
			wrong.From.Kind = "assistant"
			if _, err := b.Review(ctx, wrong); !errors.Is(err, agentlink.ErrUnauthorized) {
				t.Fatal("model approved", err)
			}
			wrong = decision
			wrong.Caller.AgentID = "another"
			if _, err := b.Review(ctx, wrong); err == nil {
				t.Fatal("another caller approved")
			}
			wrong = decision
			wrong.From.NodeID = "foreign"
			wrong.Caller.NodeID = "foreign"
			if _, err := b.Review(ctx, wrong); err == nil {
				t.Fatal("another node approved", err)
			}
			want := "failed"
			switch action {
			case "cancel":
				_, err = b.Cancel(ctx, from, accepted.ID)
				want = "cancelled"
			default:
				decision.Action = action
				_, err = b.Review(ctx, decision)
				if action == "allow_once" {
					want = "completed"
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			outcome := awaitTask(t, b, from, accepted.ID, want)
			if action == "deny" && outcome.Error != "codex_approval_denied" {
				t.Fatal(outcome)
			}
			if _, err := b.Review(ctx, decision); err == nil {
				t.Fatal("stale approval accepted")
			}
			view, err := b.Review(ctx, query)
			if err != nil || len(view.Pending) != 0 {
				t.Fatal(view, err)
			}
			_, statErr := os.Stat(marker)
			if (statErr == nil) != (action == "allow_once") {
				t.Fatal("unexpected execution", statErr)
			}
		})
	}
}

func TestAssistantMessageDeniesWithoutUserApproval(t *testing.T) {
	b := testBackend(t)
	ctx := context.Background()
	from := protocol.AgentRef{NodeID: "pot", Kind: "assistant", AgentID: "main"}
	accepted, err := submitMessageFixture(b, ctx, agentlink.TaskRequest{From: from, Target: agentlink.AssistantTarget(b.nodeID, "codex"), RequestID: "message", Text: "approve"})
	if err != nil {
		t.Fatal(err)
	}
	j, _ := b.read(ctx, from, accepted.ID)
	b.execute(ctx, j)
	if outcome := awaitTask(t, b, from, accepted.ID, "failed"); outcome.Error != "codex_approval_denied" {
		t.Fatal(outcome)
	}
	view, err := b.Review(ctx, reviewQuery(from))
	if err != nil || len(view.Pending) != 0 {
		t.Fatal(view, err)
	}
}
