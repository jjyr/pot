//go:build darwin || (linux && desktop)

package desktop

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/jjyr/pot/internal/attention"
	"github.com/jjyr/pot/internal/localui"
	"github.com/jjyr/pot/internal/systemnotifications"
)

func TestLinkedNativeAttentionRetainsReadsAfterDirectoryRemoval(t *testing.T) {
	var count atomic.Uint64
	count.Store(1)
	var deleted atomic.Bool
	local := localui.Functions{Request: func(ctx context.Context, method, path string, raw json.RawMessage) (json.RawMessage, error) {
		switch path {
		case "/api/v1/runtime":
			return json.RawMessage(`{"runtime_id":"node","boot_id":"boot"}`), nil
		case "/api/v1/assistants":
			return json.RawMessage(`[]`), nil
		case "/api/v1/agent-links":
			if deleted.Load() {
				return json.RawMessage(`{"items":[]}`), nil
			}
			return json.Marshal(map[string]any{"items": []any{map[string]any{"id": "link", "status": "connected", "assistants": []any{map[string]any{"address": map[string]string{"node_id": "peer", "kind": "assistant", "agent_id": "codex"}, "reply_id": string(rune('a' + count.Load())), "reply_count": count.Load()}}}}})
		}
		t.Errorf("unexpected native route %s", path)
		return nil, nil
	}, Events: func(ctx context.Context, path string, receive func(localui.Event) error) error {
		<-ctx.Done()
		return ctx.Err()
	}}
	manager, err := localui.Open(context.Background(), localui.Options{Local: local, LocalID: "node"})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	reads, err := attention.Open(filepath.Join(t.TempDir(), "reads.json"))
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{spaces: manager, reads: reads}
	if state, err := s.observeAttention(); err != nil || state.Unread != 0 {
		t.Fatal(state, err)
	}
	count.Store(2)
	manager.Refresh(context.Background())
	if state, err := s.observeAttention(); err != nil || state.Unread != 1 {
		t.Fatal(state, err)
	}
	owner := "agent-link:link:codex"
	nativeID, _ := localui.AttentionID(owner)
	if _, err = reads.Read(attention.Receipt{ControlID: "node", Owner: nativeID, ReplyID: "c", Count: 2}); err != nil {
		t.Fatal(err)
	}
	public := s.AssistantReads()
	if len(public) != 1 || public[0].Owner != owner {
		t.Fatal("native receipts lost frontend route", public)
	}
	deleted.Store(true)
	manager.Refresh(context.Background())
	if state, err := s.observeAttention(); err != nil || state.Unread != 0 {
		t.Fatal("deleted entry remained unread", state, err)
	}
	if len(reads.Records()) != 1 || reads.Records()[0].Owner != nativeID {
		t.Fatal("deletion erased historical receipt", reads.Records())
	}
	if _, err = reads.Read(attention.Receipt{ControlID: "node", Owner: nativeID, ReplyID: "c", Count: 2}); err == nil {
		t.Fatal("stale deleted entry acknowledged")
	}
	deleted.Store(false)
	manager.Refresh(context.Background())
	if state, err := s.observeAttention(); err != nil || state.Unread != 0 {
		t.Fatal("same identity duplicated read baseline", state, err)
	}
	if len(s.AssistantReads()) != 1 || s.AssistantReads()[0].Owner != owner {
		t.Fatal(s.AssistantReads())
	}
}

func TestLinkedNativeNotificationOwnerUsesSameIdentityBoundary(t *testing.T) {
	local := localui.Functions{Request: func(ctx context.Context, method, path string, raw json.RawMessage) (json.RawMessage, error) {
		if path == "/api/v1/runtime" {
			return json.RawMessage(`{"runtime_id":"node","boot_id":"boot"}`), nil
		}
		if path == "/api/v1/assistants" {
			return json.RawMessage(`[]`), nil
		}
		return json.RawMessage(`{"items":[]}`), nil
	}, Events: func(ctx context.Context, path string, receive func(localui.Event) error) error {
		<-ctx.Done()
		return ctx.Err()
	}}
	manager, err := localui.Open(context.Background(), localui.Options{Local: local, LocalID: "node"})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	notifications, err := systemnotifications.Open(filepath.Join(t.TempDir(), "notifications.json"))
	if err != nil {
		t.Fatal(err)
	}
	// Suppressed delivery still validates and claims the actual UI owner without
	// invoking system APIs or requiring a notification authorization dialog.
	if err = notifications.SetEnabled(false); err != nil {
		t.Fatal(err)
	}
	s := &Service{spaces: manager, notificationStore: notifications}
	event := SystemNotification{ID: "linked-reply", Space: "node", Control: "node", Owner: "agent-link:link:codex", Title: "Reply", Body: "Ready"}
	if err = s.NotifySystem(event); err != nil {
		t.Fatal("valid linked owner rejected", err)
	}
	if err = s.NotifySystem(event); err != nil {
		t.Fatal("same event did not deduplicate", err)
	}
	if claimed, err := notifications.Claim(event.ID); err != nil || claimed {
		t.Fatal("suppressed linked reply not claimed", claimed, err)
	}
	event.ID = "invalid-owner"
	event.Owner = "agent-link:link:codex:other"
	if err = s.NotifySystem(event); err == nil {
		t.Fatal("malformed linked owner accepted")
	}
	event.ID = "foreign-node"
	event.Owner = "agent-link:link:codex"
	event.Control = "other"
	if err = s.NotifySystem(event); err == nil {
		t.Fatal("notification crossed local identity boundary")
	}
}
