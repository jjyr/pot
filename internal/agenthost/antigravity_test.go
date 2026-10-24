package agenthost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

func agyBackend(t *testing.T) *backend {
	t.Helper()
	root := t.TempDir()
	db, e := storage.OpenDatabase(filepath.Join(root, "tasks.sqlite"), storage.TasksDatabase)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	runtime := Antigravity()
	runtime.Binary = fakeAgyBinary
	b, e := newBackend(db, "antigravity-test", root, runtime)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func TestAntigravityManagementKeepsSessionsAndFencesBusyEdits(t *testing.T) {
	b := agyBackend(t)
	ctx := context.Background()
	from := protocol.AgentRef{NodeID: "pot", Kind: "user", AgentID: "operator"}
	r := agentlink.AssistantManagementRequest{From: from, Action: "create", RequestID: "one", ID: "one", Name: "One", Model: "model-one", Workspace: "antigravity"}
	created, e := b.ManageAssistant(ctx, r)
	if e != nil || created.Assistant == nil {
		t.Fatal(created, e)
	}
	retry, e := b.ManageAssistant(ctx, r)
	if e != nil || retry.Assistant.ID != r.ID {
		t.Fatal(retry, e)
	}
	changed := r
	changed.Model = "model-two"
	if _, e = b.ManageAssistant(ctx, changed); !errors.Is(e, agentlink.ErrConflict) {
		t.Fatal("changed retry accepted", e)
	}
	for _, kind := range []string{"assistant", "task"} {
		bad := r
		bad.From.Kind = kind
		if _, e = b.ManageAssistant(ctx, bad); !errors.Is(e, agentlink.ErrUnauthorized) {
			t.Fatal("agent configured host", e)
		}
	}
	update := agentlink.AssistantManagementRequest{From: from, Action: "update", RequestID: "update", ID: "one", Name: "One", Model: "model-two", Revision: created.Assistant.Revision}
	b.db.Exec("UPDATE agent_adapter_assistants SET thread_id='retained-conversation' WHERE id='one'")
	a, _ := b.resolve(ctx, "one")
	parent := protocol.Envelope{ID: "incoming", Kind: "message", From: protocol.AgentRef{NodeID: "pot", Kind: "assistant", AgentID: "main"}, To: b.identity(a).Address, ConversationID: "123e4567-e89b-42d3-a456-426614174000", Text: "queued"}
	mailbox := hostMailbox{db: b.db, local: parent.To, backend: b}
	if _, e = mailbox.Deliver(ctx, parent); e != nil {
		t.Fatal(e)
	}
	if _, e = b.ManageAssistant(ctx, update); !errors.Is(e, agentlink.ErrAssistantBusy) {
		t.Fatal("queued message scope changed", e)
	}
	b.db.Exec("UPDATE agent_messages SET state='processed' WHERE id='incoming'")
	saved, e := b.ManageAssistant(ctx, update)
	if e != nil || saved.Assistant.Model != "model-two" {
		t.Fatal(saved, e)
	}
	a, _ = b.resolve(ctx, "one")
	if a.ThreadID != "retained-conversation" {
		t.Fatal("model switch replaced conversation")
	}
	update.RequestID = "stale"
	if _, e = b.ManageAssistant(ctx, update); !errors.Is(e, agentlink.ErrAssistantChanged) {
		t.Fatal("stale revision edited", e)
	}
}
func TestAntigravityPersistsIdentityBeforeDispatchAndValidatesResults(t *testing.T) {
	for _, scenario := range []string{"ok", "persistence-failure", "wrong-resume", "wrong-result", "unknown-state", "malformed", "startup-error", "cancel"} {
		t.Run(scenario, func(t *testing.T) {
			workspace := t.TempDir()
			worker, _ := os.Executable()
			s := Session{Adapter: "antigravity", Binary: fakeAgyBinary, Workspace: workspace, Mode: "read-only", Model: "model-one", JobID: "job", Worker: []string{worker}}
			if scenario == "startup-error" {
				s.Model = "start-error"
			}
			prompt := scenario
			bound := ""
			events := turnEvents{Thread: func(id string) error {
				bound = id
				if scenario == "persistence-failure" {
					return errors.New("injected database failure")
				}
				return nil
			}}
			if scenario == "wrong-resume" {
				s.ThreadID = "original-session"
				s.Model = "wrong-resume"
			}
			ctx := context.Background()
			if scenario == "cancel" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 200*time.Millisecond)
				defer cancel()
				prompt = "hold"
			}
			result := runAgyTurn(ctx, s, prompt, events)
			if result.CleanupUncertain {
				t.Fatal("supervised cleanup unconfirmed", result)
			}
			if scenario == "ok" {
				if result.Status != "completed" || result.ThreadID != bound || result.Text != "native: ok" {
					t.Fatal(result, bound)
				}
			} else if scenario == "cancel" {
				if result.Status != "cancelled" {
					t.Fatal(result)
				}
			} else if result.Status != "failed" {
				t.Fatal("invalid protocol accepted", result)
			}
			if scenario == "persistence-failure" || scenario == "wrong-resume" || scenario == "startup-error" {
				if _, e := os.Stat(filepath.Join(workspace, "received-prompt")); !os.IsNotExist(e) {
					t.Fatal("input dispatched before durable identity confirmation")
				}
			}
		})
	}
}
func TestAntigravityPolicyIsInertOutsideBoundExecutions(t *testing.T) {
	for _, mode := range []string{"", "read-only", "workspace-write"} {
		for _, tool := range []string{"view_file", "write_to_file", "run_command", "invoke_subagent"} {
			t.Setenv("POT_AGENT_MODE", mode)
			job := ""
			if mode != "" {
				job = "bound-job"
			}
			t.Setenv("POT_AGENT_JOB_ID", job)
			var out bytes.Buffer
			if e := agyPolicy(strings.NewReader(`{"toolCall":{"name":"`+tool+`","args":{}}}`), &out); e != nil {
				t.Fatal(e)
			}
			var result map[string]string
			json.Unmarshal(out.Bytes(), &result)
			want := "allow"
			if mode == "read-only" && tool != "view_file" {
				want = "deny"
			}
			if result["decision"] != want {
				t.Fatal(mode, tool, result)
			}
		}
	}
}

func TestMCPEmptyRequiredIsAnArrayForStrictClients(t *testing.T) {
	raw := encoded(toolSchema(map[string]any{}))
	var value map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		t.Fatal(err)
	}
	if string(value["required"]) != "[]" {
		t.Fatal("Antigravity hides schemas with required:null", raw)
	}
}
