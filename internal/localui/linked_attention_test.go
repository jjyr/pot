package localui

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jjyr/pot/internal/attention"
	"github.com/jjyr/pot/pkg/core"
)

func TestLinkedAttentionProjectionKeepsNativeReceiptsAndUIDirectorySeparate(t *testing.T) {
	var count atomic.Uint64
	count.Store(2)
	local := Functions{Request: func(ctx context.Context, method, path string, raw json.RawMessage) (json.RawMessage, error) {
		switch path {
		case "/api/v1/runtime":
			return json.RawMessage(`{"runtime_id":"node","boot_id":"boot"}`), nil
		case "/api/v1/assistants":
			return json.RawMessage(`[{"id":"main","reply_count":0}]`), nil
		case "/api/v1/agent-links":
			return json.Marshal(map[string]any{"items": []any{map[string]any{"id": "link", "status": "connected", "assistants": []any{map[string]any{"address": map[string]string{"node_id": "peer", "kind": "assistant", "agent_id": "codex"}, "reply_id": "reply", "reply_count": count.Load()}}}}})
		}
		t.Errorf("unexpected route %s", path)
		return nil, nil
	}, Events: func(ctx context.Context, path string, receive func(Event) error) error {
		<-ctx.Done()
		return ctx.Err()
	}}
	m, err := Open(context.Background(), Options{Local: local, LocalID: "node"})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	scope := m.Snapshot().Items[0]
	if strings.Contains(string(scope.Assistants), "agent-link:") {
		t.Fatal("native projection duplicated UI directory", string(scope.Assistants))
	}
	encoded, err := json.Marshal(scope)
	if err != nil || strings.Contains(string(encoded), "agent-link:") {
		t.Fatal("native attention leaked into browser DTO", string(encoded), err)
	}
	var rows []linkedAttention
	if err = json.Unmarshal(scope.LinkedAssistants, &rows); err != nil || len(rows) != 1 || rows[0].ID != "agent-link:link:codex" || !rows[0].Connected || rows[0].ReplyCount != 2 {
		t.Fatal(rows, err)
	}
	nativeID, ok := AttentionID(rows[0].ID)
	if !ok || !core.Identifier(nativeID) {
		t.Fatal(nativeID, ok)
	}
	if LinkedOwners(scope.LinkedAssistants)[nativeID] != rows[0].ID {
		t.Fatal("read receipt cannot restore UI identity")
	}
	receiptPath := filepath.Join(t.TempDir(), "reads.json")
	store, err := attention.Open(receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	row := attention.Row{Receipt: attention.Receipt{ControlID: "node", Owner: nativeID, ReplyID: rows[0].ReplyID, Count: rows[0].ReplyCount}, Connected: true}
	state, err := store.Observe([]attention.Row{row})
	if err != nil || state.Unread != 0 {
		t.Fatal("initial history was unread", state, err)
	}
	count.Store(3)
	m.Refresh(context.Background())
	if err = json.Unmarshal(m.Snapshot().Items[0].LinkedAssistants, &rows); err != nil {
		t.Fatal(err)
	}
	row.Count = rows[0].ReplyCount
	// A new reply ID accompanies each increasing completed reply count.
	row.ReplyID = "next-reply"
	state, err = store.Observe([]attention.Row{row})
	if err != nil || state.Unread != 1 {
		t.Fatal("linked reply did not advance native attention", state, err)
	}
	if _, err = store.Read(row.Receipt); err != nil {
		t.Fatal("linked read acknowledgement rejected", err)
	}
	reopened, err := attention.Open(receiptPath)
	if err != nil || len(reopened.Records()) != 1 || reopened.Records()[0].Owner != nativeID {
		t.Fatal("linked receipt did not survive restart", err)
	}
}
func TestLinkedAttentionIDsAreBoundedAndIsolated(t *testing.T) {
	first, ok := AttentionID(LinkedAssistantID(strings.Repeat("a", 128), strings.Repeat("b", 128)))
	if !ok || !core.Identifier(first) {
		t.Fatal(first, ok)
	}
	second, _ := AttentionID(LinkedAssistantID(strings.Repeat("a", 128), strings.Repeat("c", 128)))
	if first == second {
		t.Fatal("distinct assistants shared read receipt")
	}
	for _, invalid := range []string{"agent-link:", "agent-link:a:b:c", "agent-link:a:/path", "other:a:b"} {
		if _, ok := AttentionID(invalid); ok {
			t.Fatal("invalid synthetic owner accepted", invalid)
		}
	}
	if id, ok := AttentionID("main"); !ok || id != "main" {
		t.Fatal("local assistant identity changed", id, ok)
	}
	if !catalogDirty(Event{Data: json.RawMessage(`{"agent-links":1}`)}) || catalogDirty(Event{Data: json.RawMessage(`{"usage":1}`)}) {
		t.Fatal("peer reply changes do not refresh native attention")
	}
}
