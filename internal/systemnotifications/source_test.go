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
	"github.com/jjyr/pot/internal/testutil"
)

func TestResidentSourceQueuesDoNotBlockApprovalsOrOtherOwners(t *testing.T) {
	h := testHub(t)
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
				for _, phase := range []string{"failed", "cancelled", "complete", "interrupted"} {
					items = append(items, map[string]any{"id": phase, "sequence": cursor, "phase": phase, "title": "Build"})
				}
			}
			out = map[string]any{"cursor": cursor, "items": items}
		case strings.HasPrefix(path, "/api/v1/permissions"):
			owner, id := "beta", bApproval
			if strings.Contains(path, "assistant_id=alpha") {
				owner, id = "alpha", aApproval
			}
			out = map[string]any{"pending": []any{map[string]any{"id": id, "request": map[string]string{"owner": owner, "args": "never-notify-tool-arguments"}}}}
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
		return h.db.QueryRow("SELECT count(*) FROM notification_events").Scan(&count) == nil && count == 4
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
	var leaked int
	h.db.QueryRow("SELECT count(*) FROM notification_events WHERE body LIKE '%never-notify-tool-arguments%'").Scan(&leaked)
	if leaked != 0 {
		t.Fatal("approval arguments leaked")
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
	case <-time.After(testutil.Timeout(t, time.Second)):
		t.Fatal("missing initial business baseline")
	}
	// Wait for the initial committed baseline, not just its resource read.
	for deadline := time.Now().Add(testutil.Timeout(t, time.Second)); ; {
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
	case <-time.After(testutil.Timeout(t, time.Second)):
		t.Fatal("business event after heartbeat was lost")
	}
	for deadline := time.Now().Add(testutil.Timeout(t, time.Second)); ; {
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

func TestObserveRecoversOneFailedReadWithoutAnotherTopic(t *testing.T) {
	for _, topic := range []string{"task-outcomes", "permissions"} {
		t.Run(topic, func(t *testing.T) {
			h := testHub(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			updates := make(chan map[string]uint64, 2)
			var changed, fail atomic.Bool
			source := localui.Functions{Request: func(_ context.Context, _ string, path string, _ json.RawMessage) (json.RawMessage, error) {
				var value any
				switch {
				case path == "/api/v1/runtime":
					value = map[string]string{"runtime_id": "node", "boot_id": "boot"}
				case path == "/api/v1/assistants":
					value = []any{map[string]any{"id": "alpha", "reply_id": "old", "reply_count": 1, "capabilities": map[string]any{"version": 1, "operations": []string{"chat.read", "task.outcomes", "approvals.manage"}}}}
				case path == "/api/v1/agent-links":
					value = map[string]any{"items": []any{}}
				case strings.HasPrefix(path, "/api/v1/tasks/outcomes"):
					if topic == "task-outcomes" && fail.Swap(false) {
						return nil, errors.New("one transient Task read")
					}
					cursor := 10
					items := []any{}
					if topic == "task-outcomes" && changed.Load() {
						cursor = 11
						items = append(items, map[string]any{"id": "task", "sequence": 11, "phase": "complete", "title": "Build"})
					}
					value = map[string]any{"cursor": cursor, "items": items}
				case strings.HasPrefix(path, "/api/v1/permissions"):
					if topic == "permissions" && fail.Swap(false) {
						return nil, errors.New("one transient approval read")
					}
					id := "old"
					if topic == "permissions" && changed.Load() {
						id = "new"
					}
					value = map[string]any{"pending": []any{map[string]any{"id": id, "request": map[string]string{"owner": "alpha"}}}}
				default:
					return nil, fmt.Errorf("unexpected %s", path)
				}
				return json.Marshal(value)
			}, Events: func(ctx context.Context, _ string, receive func(localui.Event) error) error {
				for {
					select {
					case <-ctx.Done():
						return ctx.Err()
					case v := <-updates:
						data, _ := json.Marshal(v)
						if err := receive(localui.Event{Type: "changed", Data: data}); err != nil {
							return err
						}
					}
				}
			}}
			done := make(chan struct{})
			go func() { defer close(done); h.Observe(ctx, source) }()
			defer func() { cancel(); <-done }()
			updates <- map[string]uint64{"assistants": 1, "task-outcomes": 1, "permissions": 1}
			wait := func(query string, want int) {
				t.Helper()
				for deadline := time.Now().Add(testutil.Timeout(t, 2*time.Second)); ; {
					var count int
					if h.db.QueryRow(query).Scan(&count) == nil && count == want {
						return
					}
					if time.Now().After(deadline) {
						t.Fatalf("notification source did not recover without another topic: %s", topic)
					}
					time.Sleep(time.Millisecond)
				}
			}
			wait("SELECT count(*) FROM notification_sources WHERE task_cursor=10 AND approvals IS NOT NULL", 1)
			changed.Store(true)
			fail.Store(true)
			updates <- map[string]uint64{topic: 2}
			wait("SELECT count(*) FROM notification_events", 1)
		})
	}
}

func TestLinkedSourceIdentityAndRevocationFence(t *testing.T) {
	h := testHub(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	updates := make(chan map[string]uint64, 8)
	boot := "boot"
	reply := "old"
	count := 4
	cursor := 20
	approval := "old"
	status := "connected"
	revokeLate := false
	owner := "agent-link:link:codex"
	source := localui.Functions{Request: func(_ context.Context, _ string, path string, _ json.RawMessage) (json.RawMessage, error) {
		mu.Lock()
		defer mu.Unlock()
		var out any
		switch {
		case path == "/api/v1/runtime":
			out = map[string]string{"runtime_id": "node", "boot_id": boot}
		case path == "/api/v1/assistants":
			out = []any{}
		case path == "/api/v1/agent-links":
			out = map[string]any{"items": []any{map[string]any{"id": "link", "status": status, "assistants": []any{map[string]any{"address": map[string]string{"node_id": "peer", "kind": "assistant", "agent_id": "codex"}, "name": "Codex", "reply_id": reply, "reply_count": count, "preview": "Linked completed reply", "capabilities": map[string]any{"version": 1, "operations": []string{"chat.read", "task.outcomes", "approvals.manage"}}}}}}}
		case strings.HasPrefix(path, "/api/v1/tasks/outcomes"):
			if !strings.Contains(path, "assistant_id=agent-link%3Alink%3Acodex") {
				t.Fatal("linked owner lost", path)
			}
			if revokeLate {
				status = "revoked"
			}
			items := []any{}
			if strings.Contains(path, "after=") && cursor > 20 {
				items = append(items, map[string]any{"id": "linked-task", "phase": "complete", "title": "Build", "sequence": cursor})
			}
			out = map[string]any{"cursor": cursor, "items": items}
		case strings.HasPrefix(path, "/api/v1/permissions"):
			out = map[string]any{"pending": []any{map[string]any{"id": approval, "task_title": "Build", "request": map[string]any{"owner": owner, "task": "linked-task", "args": "never-send-this"}}}}
		default:
			return nil, errors.New("unexpected linked read")
		}
		data, err := json.Marshal(out)
		return data, err
	}, Events: func(ctx context.Context, _ string, receive func(localui.Event) error) error {
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case v := <-updates:
				if v == nil {
					return errors.New("synthetic stream disconnect")
				}
				data, _ := json.Marshal(v)
				if err := receive(localui.Event{Type: "changed", Data: data}); err != nil {
					return err
				}
			}
		}
	}}

	done := make(chan struct{})
	go func() { defer close(done); h.Observe(ctx, source) }()
	defer func() { cancel(); <-done }()
	wait := func(query string, want int) {
		t.Helper()
		for deadline := time.Now().Add(testutil.Timeout(t, 3*time.Second)); ; {
			var got int
			if h.db.QueryRow(query).Scan(&got) == nil && got == want {
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("linked source stalled", query, got)
			}
			time.Sleep(time.Millisecond)
		}
	}
	updates <- map[string]uint64{"agent-links": 1}
	wait("SELECT count(*) FROM notification_sources WHERE task_cursor=20 AND approvals IS NOT NULL", 1)
	mu.Lock()
	reply = "new"
	count++
	cursor++
	approval = "new"
	mu.Unlock()
	updates <- map[string]uint64{"agent-links": 2}
	wait("SELECT count(*) FROM notification_events", 3)
	rows, err := h.db.Query("SELECT body FROM notification_events")
	if err != nil {
		t.Fatal(err)
	}
	var previous Event
	for rows.Next() {
		var body string
		rows.Scan(&body)
		json.Unmarshal([]byte(body), &previous)
		if previous.Owner != owner || previous.Space != "node" || previous.Control != "node" || strings.Contains(body, "never-send-this") {
			t.Fatal(previous)
		}
	}
	rows.Close()
	mu.Lock()
	cursor++
	revokeLate = true
	mu.Unlock()
	updates <- map[string]uint64{"task-outcomes": 2}
	for deadline := time.Now().Add(testutil.Timeout(t, 2*time.Second)); h.ValidateScope("node", "node", []string{owner}); {
		if time.Now().After(deadline) {
			t.Fatal("revoked linked authority retained")
		}
		time.Sleep(time.Millisecond)
	}
	wait("SELECT count(*) FROM notification_events", 3)
	if h.Current(previous) {
		t.Fatal("revoked owner event is still current")
	}
	mu.Lock()
	revokeLate = false
	status = "connected"
	boot = "replacement"
	reply = "boot-reply"
	count++
	mu.Unlock()
	updates <- map[string]uint64{"agent-links": 3}
	wait("SELECT count(*) FROM notification_sources WHERE reply_id='boot-reply'", 1)
	wait("SELECT count(*) FROM notification_events", 3)
	// Reconnecting the same Runtime must create another authority cycle and a
	// baseline, not replay replies observed while the stream was disconnected.
	updates <- nil
	mu.Lock()
	reply = "offline-reply"
	count++
	mu.Unlock()
	updates <- map[string]uint64{"agent-links": 4}
	wait("SELECT count(*) FROM notification_sources WHERE reply_id='offline-reply'", 1)
	wait("SELECT count(*) FROM notification_events", 3)
}
