package execution

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func identity(id string) Identity {
	return Identity{Owner: "assistant", RequestID: id, Generation: 1, Role: "assistant"}
}
func TestCapacityCancellationCleanupAndUnknownFence(t *testing.T) {
	m, _ := New("r", "boot", Limits{1, 2})
	ctx := context.Background()
	entered := make(chan struct{})
	clean := make(chan struct{})
	done := make(chan Result, 1)
	go func() {
		_, r := m.Execute(ctx, identity("1"), func(ctx context.Context) Result { close(entered); <-ctx.Done(); <-clean; return Result{Cancelled, nil} })
		done <- r
	}()
	<-entered
	snapshot := m.Snapshot()
	if len(snapshot) != 1 || !m.Cancel(snapshot[0].ID) {
		t.Fatal("missing active run")
	}
	started := make(chan struct{})
	queued := make(chan Result, 1)
	go func() {
		_, r := m.Execute(ctx, identity("2"), func(context.Context) Result { close(started); return Result{Interrupted, ErrUnconfirmed} })
		queued <- r
	}()
	select {
	case <-started:
		t.Fatal("released slot before cleanup")
	case <-time.After(30 * time.Millisecond):
	}
	close(clean)
	<-done
	<-started
	if r := <-queued; r.Phase != Interrupted {
		t.Fatal(r)
	}
	if !errors.Is(m.Drain(ctx), ErrUnconfirmed) {
		t.Fatal("unknown run did not fence")
	}
	blocked, cancel := context.WithCancel(ctx)
	cancel()
	_, r := m.Execute(blocked, identity("3"), func(context.Context) Result { t.Fatal("started with cancelled context"); return Result{} })
	if !errors.Is(r.Err, context.Canceled) {
		t.Fatal(r)
	}
	m.Stop()
}
func TestStopCancelsQueueAndDedup(t *testing.T) {
	m, _ := New("r", "boot", Limits{1, 1})
	entered := make(chan struct{})
	done := make(chan struct{})
	var calls atomic.Int32
	go func() {
		m.Execute(context.Background(), identity("1"), func(ctx context.Context) Result {
			calls.Add(1)
			close(entered)
			<-ctx.Done()
			return Result{Cancelled, nil}
		})
		close(done)
	}()
	<-entered
	_, r := m.Execute(context.Background(), identity("1"), func(context.Context) Result { calls.Add(1); return Result{Succeeded, nil} })
	if !errors.Is(r.Err, ErrDuplicate) {
		t.Fatal(r)
	}
	m.Stop()
	<-done
	if err := m.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal(calls.Load())
	}
	_, r = m.Execute(context.Background(), identity("2"), func(context.Context) Result { return Result{Succeeded, nil} })
	if !errors.Is(r.Err, ErrStopping) {
		t.Fatal(r)
	}
}
func TestPanicFencesCapacity(t *testing.T) {
	m, _ := New("r", "boot", Limits{1, 1})
	e, r := m.Execute(context.Background(), identity("panic"), func(context.Context) Result { panic("test") })
	if e.Phase != Interrupted || r.Phase != Interrupted || !errors.Is(m.Drain(context.Background()), ErrUnconfirmed) {
		t.Fatal(e, r)
	}
}

func TestMaintenanceIsRecheckedAtAdmission(t *testing.T) {
	m, _ := New("runtime", "boot", Limits{1, 1})
	var checks atomic.Int32
	m.SetMaintenance(func() bool { return checks.Add(1) > 1 })
	called := false
	_, result := m.Execute(context.Background(), identity("maintenance-race"), func(context.Context) Result { called = true; return Result{Succeeded, nil} })
	if called || !errors.Is(result.Err, ErrStopping) || len(m.Snapshot()) != 0 {
		t.Fatal("work crossed the maintenance admission barrier", called, result)
	}
}

func TestRemovedSummaryRoleCannotEnterManager(t *testing.T) {
	m, err := New("runtime", "boot", Limits{1, 1})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Stop()
	id := identity("notification")
	id.Role = "summary"
	_, result := m.Execute(context.Background(), id, func(context.Context) Result { t.Fatal("obsolete execution role admitted"); return Result{} })
	if result.Err == nil || len(m.Snapshot()) != 0 {
		t.Fatal("obsolete role was accepted", result)
	}
}
