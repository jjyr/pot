package control

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/assistant"
	"github.com/jjyr/pot/internal/usage"
	"github.com/jjyr/pot/pkg/agent"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/execution"
	m "github.com/jjyr/pot/pkg/modelprotocol"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

func mainRecoveryFixture(t *testing.T, issued, known, proof bool, boot string) (*Service, *assistant.Conversation) {
	t.Helper()
	ctx := context.Background()
	s := taskControl(t, "runtime", "")
	if _, err := s.Registry.Create(ctx, "create", "Pot"); err != nil {
		t.Fatal(err)
	}
	c, err := s.Conversation(ctx, "main")
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Configure(ctx, taskConfig(t), 0); err != nil {
		t.Fatal(err)
	}
	if err = c.Close(); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	delete(s.conversations, "main")
	locked := true
	defer func() {
		if locked {
			s.mu.Unlock()
		}
	}()
	// Keep background receivers from reopening this externally seeded fixture
	// before the complete original turn and receipt journal are persisted.
	store, err := storage.Open(filepath.Join(s.options.Root, "assistants", "main", "conversation.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	turn := map[string]any{"owner": "main", "boot_id": boot, "generation": 1, "request": map[string]any{"request_id": "original", "conversation_id": "main", "text": "original"}, "user_message_id": "original-user", "state": "running", "cancelling": false, "paused": true, "resuming": false, "reply_message_id": ""}
	raw, _ := json.Marshal(turn)
	if err = store.Set(ctx, "conversation.turn", string(raw)); err != nil {
		t.Fatal(err)
	}
	request := "original"
	if _, err = store.Save(ctx, protocol.Message{ID: "original-user", RequestID: &request, ConversationID: "main", Role: protocol.User, Text: "original", Status: protocol.Failed, CreatedAtMS: 1}); err != nil {
		t.Fatal(err)
	}
	checkpoint := agent.Checkpoint{Type: "context", Version: 2, Messages: []m.Message{}, Pending: []m.ToolCall{}, Receipts: []agent.ToolReceipt{}, SteeringIDs: []string{}, LastRequest: request}
	if issued {
		id := "command"
		checkpoint.Started = &id
		checkpoint.Pending = []m.ToolCall{{ID: id, Name: "exec_command", Arguments: json.RawMessage(`{"cmd":"never replay"}`)}}
		op := Operation{Owner: "main", Turn: request, Generation: 1, CallID: id, Target: core.RuntimeTarget("runtime"), Tool: "exec_command", Args: checkpoint.Pending[0].Arguments, Worker: false}
		receipt, _, e := s.Operations.Begin(ctx, op)
		if e != nil {
			t.Fatal(e)
		}
		if known {
			err = s.Operations.Complete(ctx, receipt.ID, json.RawMessage(`{"stdout":"original known result"}`), nil)
		} else {
			err = s.Operations.Unknown(ctx, receipt.ID)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	raw, _ = json.Marshal(checkpoint)
	if err = store.SaveContext(ctx, raw); err != nil {
		t.Fatal(err)
	}
	if proof {
		raw, _ = json.Marshal([]any{"main", request, uint64(1), boot})
		if err = store.Set(ctx, "conversation.cleanup.original", string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	store.Close()
	locked = false
	s.mu.Unlock()
	c, err = s.Conversation(ctx, "main")
	if err != nil {
		t.Fatal(err)
	}
	return s, c
}
func TestMainRecoveryOwnerSafeBoundaryAndOriginalKnownReceipt(t *testing.T) {
	for _, issued := range []bool{false, true} {
		t.Run(map[bool]string{false: "safe-paused", true: "known-receipt"}[issued], func(t *testing.T) {
			s, c := mainRecoveryFixture(t, issued, true, issued, "previous-boot")
			ctx := context.Background()
			_, result := s.options.Manager.Execute(ctx, execution.Identity{Owner: "main", RequestID: "original", Generation: 1, Role: "assistant"}, func(context.Context) execution.Result {
				return execution.Result{Phase: execution.Interrupted, Err: core.ErrCleanupUnconfirmed}
			})
			if result.Phase != execution.Interrupted {
				t.Fatal(result)
			}
			inspected, err := s.RecoverMain(ctx, "main", MainRecoveryCommand{Action: "inspect"})
			if err != nil || inspected.UserMessageID != "original-user" || inspected.Reconciled {
				t.Fatal(inspected, err)
			}
			if _, err = s.RecoverMain(ctx, "main", MainRecoveryCommand{Action: "reconcile", UserMessageID: "different"}); err == nil {
				t.Fatal("wrong original user message accepted")
			}
			recovered, err := s.RecoverMain(ctx, "main", MainRecoveryCommand{Action: "reconcile", UserMessageID: inspected.UserMessageID})
			if err != nil || !recovered.Reconciled {
				t.Fatal(recovered, err)
			}
			// Model a retry after the durable conversation receipt exists but the
			// original volatile fence/wait completion still needs attention.
			_, result = s.options.Manager.Execute(ctx, execution.Identity{Owner: "main", RequestID: "original", Generation: 1, Role: "assistant"}, func(context.Context) execution.Result { return execution.Result{Phase: execution.Interrupted} })
			if err = s.Usage.Wait(ctx, usage.WaitRequest{Scope: "main", Turn: "original", AtMS: time.Now().UnixMilli(), Required: 2}); err != nil {
				t.Fatal(err)
			}
			again, err := s.RecoverMain(ctx, "main", MainRecoveryCommand{Action: "reconcile", UserMessageID: inspected.UserMessageID})
			if err != nil || !again.Reconciled {
				t.Fatal("exact retry failed", again, err)
			}
			if waiting, e := s.Usage.Waiting(ctx, "main"); e != nil || waiting != nil {
				t.Fatal("exact recovered wait not cleared", waiting, e)
			}
			nextCtx, cancel := context.WithTimeout(ctx, time.Second)
			defer cancel()
			_, result = s.options.Manager.Execute(nextCtx, execution.Identity{Owner: "next", RequestID: "next", Generation: 1, Role: "assistant"}, func(context.Context) execution.Result { return execution.Result{Phase: execution.Succeeded} })
			if result.Phase != execution.Succeeded {
				t.Fatal("recovered Main capacity stayed fenced", result)
			}
			if err = c.Configure(ctx, taskConfig(t), 1); err != nil {
				t.Fatal("Main stayed unusable", err)
			}
		})
	}
}
func TestMainRecoveryMissingReceiptAndOldHostCleanupStayFenced(t *testing.T) {
	for _, known := range []bool{false, true} {
		t.Run(map[bool]string{false: "unknown-effect", true: "unproven-cleanup"}[known], func(t *testing.T) {
			s, c := mainRecoveryFixture(t, true, known, false, "previous-boot")
			ctx := context.Background()
			out, err := s.RecoverMain(ctx, "main", MainRecoveryCommand{Action: "reconcile", UserMessageID: "original-user"})
			if err != nil || out.Reconciled || len(out.Unresolved) == 0 {
				t.Fatal(out, err)
			}
			if known && !strings.Contains(strings.Join(out.Unresolved, " "), "previous-boot") {
				t.Fatal(out)
			}
			if err = c.Configure(ctx, taskConfig(t), 1); err == nil {
				t.Fatal("unproven Main unblocked")
			}
		})
	}
}
