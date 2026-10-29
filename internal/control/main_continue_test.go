package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jjyr/pot/internal/gateway"
	"github.com/jjyr/pot/pkg/agent"
	m "github.com/jjyr/pot/pkg/modelprotocol"
	"github.com/jjyr/pot/pkg/protocol"
)

func TestContinueMainReconcilesCrashAndResumesOriginalProgress(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, c := mainRecoveryFixture(t, true, true, true, "previous-boot", func(checkpoint *agent.Checkpoint) {
		checkpoint.Messages = []m.Message{m.TextMessage("user", "original goal"), m.TextMessage("assistant", "")}
		checkpoint.Messages[1].Calls = append([]m.ToolCall(nil), checkpoint.Pending...)
	})
	var calls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		var request struct {
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		if err := json.Unmarshal(body, &request); err != nil {
			t.Error("continuation request not JSON", err)
			return
		}
		var userGoals int
		var toolHasResult bool
		for _, message := range request.Messages {
			switch message.Role {
			case "user":
				var content string
				if json.Unmarshal(message.Content, &content) == nil && content == "original goal" {
					userGoals++
				}
			case "tool":
				if strings.Contains(string(message.Content), "original known result") {
					toolHasResult = true
				}
			}
		}
		if userGoals != 1 || !toolHasResult {
			t.Error("continuation lost saved progress", string(body))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"continued\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer upstream.Close()
	if err := s.Providers.Save(ctx, gateway.Provider{ID: "fixture", Endpoint: upstream.URL, Protocol: "openai_chat", AllowedHosts: []string{"127.0.0.1"}, SecretRef: "provider/fixture"}, []byte("fixture")); err != nil {
		t.Fatal(err)
	}
	before, _ := c.Snapshot(ctx)
	if before.Continuation == nil || !before.Continuation.RecoveryRequired || before.Continuation.UserMessageID != "original-user" {
		t.Fatal(before)
	}
	command := ContinueMainCommand{RequestID: "continue", UserMessageID: "original-user", AttemptID: "original"}
	accepted, err := s.ContinueMain(ctx, "main", command)
	if err != nil || accepted.MessageID != "original-user" {
		t.Fatal(accepted, err)
	}
	after := awaitConversation(t, c, func(snapshot protocol.Snapshot) bool {
		return snapshot.Turn == nil && len(snapshot.Messages) > 1 && snapshot.Messages[len(snapshot.Messages)-1].Text == "continued"
	})
	if after.Continuation != nil || calls.Load() != 1 {
		t.Fatal(after, calls.Load())
	}
	if duplicate, err := s.ContinueMain(ctx, "main", command); err != nil || !duplicate.Duplicate || calls.Load() != 1 {
		t.Fatal(duplicate, err, calls.Load())
	}
	records, err := s.Operations.RecoveryRecords(ctx, "main", "original")
	if err != nil || len(records) != 1 || records[0].Receipt.State != "complete" {
		t.Fatal("original effect was replayed", records, err)
	}
}

func TestContinueMainRejectsUnresolvedEffectsAndStaleTarget(t *testing.T) {
	t.Parallel()
	s, c := mainRecoveryFixture(t, true, false, true, "previous-boot")
	ctx := context.Background()
	if _, err := s.ContinueMain(ctx, "main", ContinueMainCommand{RequestID: "wrong-target", UserMessageID: "different", AttemptID: "original"}); err == nil {
		t.Fatal("stale continuation accepted")
	}
	_, err := s.ContinueMain(ctx, "main", ContinueMainCommand{RequestID: "continue", UserMessageID: "original-user", AttemptID: "original"})
	var api protocol.APIError
	if !errors.As(err, &api) || api.Code != "turn_recovery_required" {
		t.Fatal(err)
	}
	if _, err := c.Recovery(ctx); err != nil {
		t.Fatal("unknown effect silently abandoned", err)
	}
	records, err := s.Operations.RecoveryRecords(ctx, "main", "original")
	if err != nil || len(records) != 1 || records[0].Receipt.State != "unknown" {
		t.Fatal(records, err)
	}
}
