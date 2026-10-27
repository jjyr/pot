package agenthost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/protocol"
)

// These are fake native processes, with a file handshake instead of a model.
func TestConfigurationSavesDuringNativeExecutionPreserveSessionAndInbox(t *testing.T) {
	for _, writer := range []string{"management", "owner"} {
		t.Run(writer, func(t *testing.T) {
			b := testBackend(t)
			ctx, cancel := context.WithCancel(t.Context())
			a, err := b.resolve(ctx, "codex")
			if err != nil {
				t.Fatal(err)
			}
			a.Model = "model-one"
			a.Effort = "high"
			if err = b.ownerConfigure(ctx, a); err != nil {
				t.Fatal(err)
			}
			done := make(chan struct{})
			go func() { b.run(ctx); close(done) }()
			defer func() { cancel(); <-done }()
			from := protocol.AgentRef{NodeID: "settings-fixture", Kind: "user", AgentID: "operator"}
			first, err := submitMessageFixture(b, ctx, agentlink.TaskRequest{From: from, RequestID: "active", Text: "hold-after-answer"})
			if err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(10 * time.Second)
			for {
				if _, err = os.Stat(filepath.Join(a.Workspace, "answer-ready")); err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("fake native did not reach held execution")
				}
				time.Sleep(10 * time.Millisecond)
			}
			active, err := b.read(ctx, from, first.ID)
			if err != nil || active.Task.State != "running" || active.TurnID == "" {
				t.Fatal(active, err)
			}
			next, err := submitMessageFixture(b, ctx, agentlink.TaskRequest{From: from, RequestID: "next", Text: "next execution"})
			if err != nil {
				t.Fatal(err)
			}
			last, err := submitMessageFixture(b, ctx, agentlink.TaskRequest{From: from, RequestID: "last", Text: "last execution"})
			if err != nil {
				t.Fatal(err)
			}
			queued := func() string {
				rows, e := b.db.QueryContext(ctx, "SELECT sequence,id,data FROM agent_adapter_jobs WHERE id IN (?,?) ORDER BY sequence", next.ID, last.ID)
				if e != nil {
					t.Fatal(e)
				}
				defer rows.Close()
				var result string
				for rows.Next() {
					var sequence int
					var id, data string
					if e = rows.Scan(&sequence, &id, &data); e != nil {
						t.Fatal(e)
					}
					result += fmt.Sprint(sequence, id, data)
				}
				if e = rows.Err(); e != nil {
					t.Fatal(e)
				}
				return result
			}
			beforeJobs := queued()
			a, err = b.resolve(ctx, "codex")
			if err != nil {
				t.Fatal(err)
			}
			mailbox := hostMailbox{db: b.db, backend: b, local: b.identity(a).Address}
			for _, id := range []string{"pending-one", "pending-two"} {
				if _, err = mailbox.Deliver(ctx, protocol.Envelope{ID: id, Kind: "message", From: from, To: b.identity(a).Address, ConversationID: "123e4567-e89b-42d3-a456-426614174000", Text: id}); err != nil {
					t.Fatal(err)
				}
			}
			inbox := func() string {
				rows, e := b.db.QueryContext(ctx, "SELECT sequence,id,state,envelope FROM agent_messages WHERE direction='incoming' ORDER BY sequence")
				if e != nil {
					t.Fatal(e)
				}
				defer rows.Close()
				var result string
				for rows.Next() {
					var sequence int
					var id, state, envelope string
					if e = rows.Scan(&sequence, &id, &state, &envelope); e != nil {
						t.Fatal(e)
					}
					result += fmt.Sprint(sequence, id, state, envelope)
				}
				if e = rows.Err(); e != nil {
					t.Fatal(e)
				}
				return result
			}
			beforeInbox := inbox()
			settings := func(model, effort string) {
				raw, e := os.ReadFile(filepath.Join(a.Workspace, "native-settings.json"))
				if e != nil {
					t.Fatal(e)
				}
				var value map[string]string
				if json.Unmarshal(raw, &value) != nil || value["model"] != model || value["effort"] != effort {
					t.Fatalf("native execution settings: %s, expected %s/%s", raw, model, effort)
				}
			}
			settings("model-one", "high")
			if writer == "management" {
				saved, e := b.ManageAssistant(ctx, agentlink.AssistantManagementRequest{From: from, Action: "update", ID: a.ID, RequestID: "save-while-running", Revision: a.Revision, Model: managementValue("model-two"), Effort: managementValue("")})
				if e != nil || saved.Assistant.Model != "model-two" || saved.Assistant.Effort != "" {
					t.Fatal(saved, e)
				}
			} else {
				a.Model = "model-two"
				a.Effort = ""
				if err = b.ownerConfigure(ctx, a); err != nil {
					t.Fatal(err)
				}
			}
			persisted, err := b.resolve(ctx, a.ID)
			if err != nil || persisted.Model != "model-two" || persisted.Effort != "" {
				t.Fatal(persisted, err)
			}
			stillActive, err := b.read(ctx, from, first.ID)
			if err != nil || stillActive.Task.State != "running" || stillActive.TurnID != active.TurnID {
				t.Fatal("save restarted or stopped active execution", stillActive, err)
			}
			settings("model-one", "high")
			if inbox() != beforeInbox || queued() != beforeJobs {
				t.Fatal("save changed pending inbox or queued jobs/order")
			}
			if err = b.DeleteAssistant(ctx, agentlink.DeleteAssistantRequest{From: from, AssistantID: a.ID, Revision: persisted.Revision}); !errors.Is(err, agentlink.ErrAssistantBusy) {
				t.Fatal("active deletion fence lost", err)
			}
			invalid := persisted
			invalid.Mode = "workspace-write"
			if err = b.ownerConfigure(ctx, invalid); !errors.Is(err, agentlink.ErrConflict) {
				t.Fatal("permission scope changed", err)
			}
			invalid = persisted
			invalid.Workspace = t.TempDir()
			if err = b.ownerConfigure(ctx, invalid); !errors.Is(err, agentlink.ErrConflict) {
				t.Fatal("workspace scope changed", err)
			}
			if err = os.WriteFile(filepath.Join(a.Workspace, "complete-turn"), nil, 0600); err != nil {
				t.Fatal(err)
			}
			awaitTask(t, b, from, first.ID, "completed")
			nextDone := awaitTask(t, b, from, next.ID, "completed")
			lastDone := awaitTask(t, b, from, last.ID, "completed")
			if nextDone.UpdatedAt.After(lastDone.UpdatedAt) {
				t.Fatal("queued execution order changed")
			}
			settings("model-two", "")
			if inbox() != beforeInbox {
				t.Fatal("configuration or execution cleared/reordered pending inbox")
			}
		})
	}
}
