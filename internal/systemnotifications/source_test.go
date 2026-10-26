package systemnotifications

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/localui"
	"github.com/jjyr/pot/pkg/agentlink"
)

// Batch driver for deterministic source contracts; all event decisions and
// cursor commits use the same functions as the resident independent queues.
func (h *Hub) readSources(ctx context.Context, source localui.Caller, states map[string]sourceState, topics map[string]bool, reset bool) error {
	directory, err := readDirectory(ctx, source)
	if err != nil {
		return err
	}
	active := map[string]Owner{}
	for _, owner := range directory.Owners {
		active[owner.ID] = owner
		before, known := states[owner.ID]
		fresh := reset || !known || before.Boot != sourceBoot(directory, owner)
		next := before
		if fresh {
			next = sourceState{Boot: sourceBoot(directory, owner)}
		}
		events := replyEvents(owner, before, fresh)
		if len(events) > 0 && !sourceCurrent(ctx, source, owner, directory.Boot, "") {
			return ErrSourceChanged
		}
		next.Reply = owner.ReplyID
		next.Count = owner.ReplyCount
		if err = h.commitSource(ctx, owner.ID, next, events, "reply"); err != nil {
			return err
		}
		states[owner.ID] = next
		if owner.Capabilities.Supports(agentlink.TaskOutcomes) && (fresh || topics["task-outcomes"] || topics["agent-links"]) {
			next.Cursor, err = taskRead(ctx, source, owner, directory.Boot, next.Cursor, func(cursor *uint64, events []Event) error {
				return h.commitSource(ctx, owner.ID, sourceState{Boot: next.Boot, Cursor: cursor}, events, "task")
			})
			if errors.Is(err, ErrSourceChanged) {
				return err
			}
			states[owner.ID] = next
		}
		if owner.Capabilities.Supports(agentlink.Approvals) && (fresh || topics["permissions"] || topics["agent-links"]) {
			next.Approvals, err = approvalRead(ctx, source, owner, directory.Boot, next.Approvals, func(approvals map[string]bool, events []Event) error {
				return h.commitSource(ctx, owner.ID, sourceState{Boot: next.Boot, Approvals: approvals}, events, "approval")
			})
			if errors.Is(err, ErrSourceChanged) {
				return err
			}
			states[owner.ID] = next
		}
	}
	for id := range states {
		if _, ok := active[id]; !ok {
			delete(states, id)
		}
	}
	h.mu.Lock()
	h.control = directory.Control
	h.owners = active
	h.mu.Unlock()
	return nil
}

func TestResidentSourceQueuesDoNotBlockApprovalsOrOtherOwners(t *testing.T) {
	h := testHub(t)
	device(t, h, "q", "push")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	alphaActive := true
	blockAlpha := false
	alphaCursor, betaCursor := 10, 10
	alphaApproval, betaApproval := "old-a", "old-b"
	updates := make(chan map[string]uint64, 8)
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	source := localui.Functions{Request: func(call context.Context, _ string, path string, _ json.RawMessage) (json.RawMessage, error) {
		mu.Lock()
		active, block, aCursor, bCursor, aApproval, bApproval := alphaActive, blockAlpha, alphaCursor, betaCursor, alphaApproval, betaApproval
		mu.Unlock()
		var out any
		switch {
		case path == "/api/v1/runtime":
			out = map[string]string{"runtime_id": "node", "boot_id": "boot"}
		case path == "/api/v1/assistants":
			var owners []any
			for _, id := range []string{"alpha", "beta"} {
				if id == "alpha" && !active {
					continue
				}
				owners = append(owners, map[string]any{"id": id, "name": id, "reply_id": "old", "reply_count": 1, "capabilities": map[string]any{"version": 1, "operations": []string{"chat.read", "task.outcomes", "approvals.manage"}}})
			}
			out = owners
		case path == "/api/v1/agent-links":
			out = map[string]any{"items": []any{}}
		case strings.HasPrefix(path, "/api/v1/tasks/outcomes"):
			alpha := strings.Contains(path, "assistant_id=alpha")
			cursor := bCursor
			if alpha {
				cursor = aCursor
			}
			if alpha && block && strings.Contains(path, "after=") {
				select {
				case started <- struct{}{}:
				default:
				}
				select {
				case <-call.Done():
					return nil, call.Err()
				case <-release:
				}
			}
			items := []any{}
			if cursor > 10 && strings.Contains(path, "after=10") {
				items = append(items, map[string]any{"id": "task", "sequence": cursor, "phase": "complete", "title": "Build"})
			}
			out = map[string]any{"cursor": cursor, "items": items}
		case strings.HasPrefix(path, "/api/v1/permissions"):
			owner, id := "beta", bApproval
			if strings.Contains(path, "assistant_id=alpha") {
				owner, id = "alpha", aApproval
			}
			out = map[string]any{"pending": []any{map[string]any{"id": id, "request": map[string]string{"owner": owner}}}}
		default:
			return nil, errors.New("unexpected resident source path")
		}
		data, err := json.Marshal(out)
		return data, err
	}, Events: func(stream context.Context, _ string, receive func(localui.Event) error) error {
		for {
			select {
			case <-stream.Done():
				return stream.Err()
			case topics := <-updates:
				data, _ := json.Marshal(topics)
				if err := receive(localui.Event{Type: "changed", Data: data}); err != nil {
					return err
				}
			}
		}
	}}
	done := make(chan struct{})
	go func() { h.Observe(ctx, source); close(done) }()
	updates <- map[string]uint64{"assistants": 1, "task-outcomes": 1, "permissions": 1}
	eventually := func(check func() bool) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for !check() {
			if time.Now().After(deadline) {
				t.Fatal("resident source did not progress")
			}
			time.Sleep(time.Millisecond)
		}
	}
	eventually(func() bool {
		var count int
		return h.db.QueryRow("SELECT count(*) FROM notification_sources WHERE task_cursor=10 AND approvals IS NOT NULL").Scan(&count) == nil && count == 2
	})
	mu.Lock()
	blockAlpha = true
	alphaCursor = 11
	mu.Unlock()
	updates <- map[string]uint64{"assistants": 1, "task-outcomes": 2, "permissions": 1}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("slow Task read was not started")
	}
	mu.Lock()
	alphaApproval = "new-a"
	betaApproval = "new-b"
	betaCursor = 11
	mu.Unlock()
	updates <- map[string]uint64{"assistants": 1, "task-outcomes": 3, "permissions": 2}
	// New approvals and another owner's Task progress while alpha's earlier
	// outcome response is still suspended. No extra PWA observer is involved.
	eventually(func() bool {
		var count int
		return h.db.QueryRow("SELECT count(*) FROM notification_events").Scan(&count) == nil && count == 3
	})
	var alphaTasks int
	h.db.QueryRow("SELECT count(*) FROM notification_events WHERE json_extract(body,'$.owner')='alpha' AND json_extract(body,'$.kind')='task-complete'").Scan(&alphaTasks)
	if alphaTasks != 0 {
		t.Fatal("suspended Task result was prematurely published")
	}
	// Removal cancels that owner's in-flight read and queued invalidations.
	mu.Lock()
	alphaActive = false
	mu.Unlock()
	updates <- map[string]uint64{"assistants": 2, "task-outcomes": 3, "permissions": 2}
	eventually(func() bool { return !h.ValidateScope("node", "node", []string{"alpha"}) })
	close(release)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("source shutdown retained a suspended reader")
	}
	h.db.QueryRow("SELECT count(*) FROM notification_events WHERE json_extract(body,'$.owner')='alpha' AND json_extract(body,'$.kind')='task-complete'").Scan(&alphaTasks)
	if alphaTasks != 0 {
		t.Fatal("late deleted-owner outcome notified")
	}
}

// The named heartbeat emitted by the transport is liveness, not a version map.
func TestResidentSourceIgnoresHeartbeatWithoutLosingBusinessVersions(t *testing.T) {
	h := testHub(t)
	ctx, cancel := context.WithCancel(context.Background())
	events := make(chan localui.Event, 8)
	reads := make(chan struct{}, 8)
	var count atomic.Uint64
	count.Store(1)
	source := localui.Functions{Request: func(_ context.Context, _ string, path string, _ json.RawMessage) (json.RawMessage, error) {
		var value any
		switch path {
		case "/api/v1/runtime":
			value = map[string]string{"runtime_id": "node", "boot_id": "boot"}
		case "/api/v1/assistants":
			reads <- struct{}{}
			n := count.Load()
			value = []any{map[string]any{"id": "alpha", "name": "Alpha", "reply_id": fmt.Sprint(n), "reply_count": n}}
		case "/api/v1/agent-links":
			value = map[string]any{"items": []any{}}
		default:
			return nil, fmt.Errorf("unexpected path %s", path)
		}
		return json.Marshal(value)
	}, Events: func(ctx context.Context, _ string, receive func(localui.Event) error) error {
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case event := <-events:
				if err := receive(event); err != nil {
					return err
				}
			}
		}
	}}
	done := make(chan struct{})
	go func() { defer close(done); h.Observe(ctx, source) }()
	defer func() { cancel(); <-done }()
	events <- localui.Event{Type: "changed", Data: []byte(`{"assistants":1}`)}
	select {
	case <-reads:
	case <-time.After(time.Second):
		t.Fatal("missing initial business baseline")
	}
	// Wait for the initial committed baseline, not just its resource read.
	for deadline := time.Now().Add(time.Second); ; {
		var n int
		if h.db.QueryRow("SELECT count(*) FROM notification_sources").Scan(&n) == nil && n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("baseline not committed")
		}
		time.Sleep(time.Millisecond)
	}
	events <- localui.Event{Type: "heartbeat", Data: []byte(`{}`)}
	events <- localui.Event{Type: "unrelated", Data: []byte(`{"assistants":20}`)}
	events <- localui.Event{Type: "changed", Data: []byte(`{"assistants":1}`)}
	select {
	case <-reads:
		t.Fatal("heartbeat/unrelated frame invalidated business versions")
	case <-time.After(150 * time.Millisecond):
	}
	count.Store(2)
	events <- localui.Event{Type: "changed", Data: []byte(`{"assistants":2}`)}
	select {
	case <-reads:
	case <-time.After(time.Second):
		t.Fatal("business event after heartbeat was lost")
	}
	for deadline := time.Now().Add(time.Second); ; {
		var n int
		if h.db.QueryRow("SELECT count(*) FROM notification_events").Scan(&n) == nil && n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("new reply after heartbeat not published")
		}
		time.Sleep(time.Millisecond)
	}
}
