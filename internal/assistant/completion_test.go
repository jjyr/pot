package assistant

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/execution"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

func completionOptions(t *testing.T, boot string, calls *atomic.Int32) ConversationOptions {
	t.Helper()
	manager, err := execution.New("runtime", boot, execution.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	spec := resolved(t, "model")
	spec.Params.Agent.AutoCompact = false
	spec.Params.Agent.BuiltinTools = []string{}
	if err = spec.Rehash(); err != nil {
		t.Fatal(err)
	}
	options := ConversationOptions{Owner: "main", RuntimeID: "runtime", BootID: boot, Manager: manager, InitialSpec: &spec}
	options.Prepare = func(context.Context, core.Thread, core.TurnRequest) (RunOptions, error) {
		return RunOptions{Execution: core.ExecutionContext{Owner: "main", Role: core.Main}, Cleanup: func(context.Context, core.ExecutionContext, bool) error { return nil }, Services: modelPort{model: func(context.Context, core.ModelRequest) (io.ReadCloser, error) {
			calls.Add(1)
			return replySSE("finished"), nil
		}}}, nil
	}
	return options
}

func TestCompletionRetriesSQLiteBusyWithoutReplayingAndKeepsSubscription(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	db, err := storage.OpenDatabase(filepath.Join(root, "tasks.sqlite"), storage.TasksDatabase)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mail, err := OpenMailbox(db, "runtime")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("PRAGMA busy_timeout=25"); err != nil {
		t.Fatal(err)
	}
	other, err := storage.OpenDatabase(filepath.Join(root, "tasks.sqlite"), storage.TasksDatabase)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	lock, err := other.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback()
	var calls atomic.Int32
	options := completionOptions(t, "boot", &calls)
	options.OnFinished = func(ctx context.Context, r protocol.SendMessage, result execution.Result) error {
		return mail.FinishChat(ctx, "main", r.RequestID, "", result.Phase == execution.Succeeded)
	}
	c, err := OpenConversation(filepath.Join(root, "conversation.sqlite"), options)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	updates, remove, err := c.SubscribeUI(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer remove()
	accepted, err := c.Send(ctx, protocol.SendMessage{RequestID: "first", ConversationID: "main", Text: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	waitSnapshot(t, c, func(s protocol.Snapshot) bool {
		return s.Agent.Detail != nil && strings.Contains(*s.Agent.Detail, "SQLITE_BUSY")
	})
	frame, err := c.UIFrame(ctx, 0, true)
	if err != nil || len(frame.Replies) != 1 || frame.Replies[0].State != "unknown" || frame.Replies[0].InputID != accepted.MessageID {
		t.Fatal(frame, err)
	}
	cat, err := c.Catalogue(ctx)
	if err != nil || cat.Executing || !cat.Failed {
		t.Fatal(cat, err)
	}
	select {
	case <-c.done:
		t.Fatal("completion failure killed driver")
	default:
	}
	if _, err = c.Send(ctx, protocol.SendMessage{RequestID: "next", ConversationID: "main", Text: "next"}); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("replayed completed invocation", calls.Load())
	}
	if err = lock.Rollback(); err != nil {
		t.Fatal(err)
	}
	waitSnapshot(t, c, func(s protocol.Snapshot) bool { return s.Turn == nil && len(s.Messages) >= 4 })
	if err := frame.Validate(); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatal("queued input did not execute exactly once", calls.Load())
	}
	frame, err = c.UIFrame(ctx, 0, true)
	if err != nil || frame.Agent.Detail != nil {
		t.Fatal(frame, err)
	}
	for _, reply := range frame.Replies {
		if reply.State != "completed" {
			t.Fatal(reply)
		}
	}
	// The same live subscription remains open after recovery.
	for draining := true; draining; {
		select {
		case _, ok := <-updates:
			if !ok {
				t.Fatal("subscription closed")
			}
		default:
			draining = false
		}
	}
	var receipt string
	if err = db.Ping(); err != nil {
		t.Fatal(err)
	}
	if err = c.store.DB().QueryRow("SELECT value FROM metadata WHERE key='conversation.completion'").Scan(&receipt); err != nil || receipt != "null" {
		t.Fatal(receipt, err)
	}
}

func TestCompletionRestartsPersistenceWithoutReplayingModel(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[failed], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "conversation.sqlite")
			var calls atomic.Int32
			options := completionOptions(t, "first-boot", &calls)
			if failed {
				options.Prepare = func(context.Context, core.Thread, core.TurnRequest) (RunOptions, error) {
					return RunOptions{}, errors.New("preparation failed")
				}
			}
			options.OnFinished = func(context.Context, protocol.SendMessage, execution.Result) error {
				return errors.New("fixture completion unavailable")
			}
			c, err := OpenConversation(path, options)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = c.Send(context.Background(), protocol.SendMessage{RequestID: "first", ConversationID: "main", Text: "hello"}); err != nil {
				t.Fatal(err)
			}
			waitSnapshot(t, c, func(s protocol.Snapshot) bool {
				return s.Agent.Detail != nil && strings.Contains(*s.Agent.Detail, "completion unavailable")
			})
			if _, err = c.Send(context.Background(), protocol.SendMessage{RequestID: "next", ConversationID: "main", Text: "next"}); err != nil {
				t.Fatal(err)
			}
			if err = c.Close(); err != nil {
				t.Fatal(err)
			}
			before := calls.Load()
			options = completionOptions(t, "second-boot", &calls)
			var receipts atomic.Int32
			options.OnFinished = func(_ context.Context, r protocol.SendMessage, result execution.Result) error {
				if r.RequestID == "first" {
					receipts.Add(1)
					want := execution.Succeeded
					if failed {
						want = execution.Failed
					}
					if result.Phase != want {
						t.Errorf("lost original completion: %+v", result)
					}
				}
				return nil
			}
			c, err = OpenConversation(path, options)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			finished := waitSnapshot(t, c, func(s protocol.Snapshot) bool { return s.Turn == nil && calls.Load() > before })
			if calls.Load() != before+1 || receipts.Load() != 1 {
				t.Fatal("replayed original execution or lost queued input", calls.Load(), before, receipts.Load())
			}
			if failed {
				count := 0
				for _, m := range finished.Messages {
					if m.Role == protocol.Assistant && m.Error == "preparation failed" {
						count++
					}
				}
				if count != 1 {
					t.Fatal("duplicate failure replies", count)
				}
			}
		})
	}
}

func TestCompletionFailureShutdownIsBounded(t *testing.T) {
	var calls atomic.Int32
	options := completionOptions(t, "boot", &calls)
	options.OnFinished = func(ctx context.Context, _ protocol.SendMessage, _ execution.Result) error {
		return errors.New("fixture storage unavailable")
	}
	c, err := OpenConversation(filepath.Join(t.TempDir(), "conversation.sqlite"), options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Send(context.Background(), protocol.SendMessage{RequestID: "first", ConversationID: "main", Text: "hello"}); err != nil {
		t.Fatal(err)
	}
	waitSnapshot(t, c, func(s protocol.Snapshot) bool { return s.Agent.Detail != nil })
	done := make(chan error, 1)
	go func() { done <- c.Close() }()
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("retry backoff blocked shutdown")
	}
}
