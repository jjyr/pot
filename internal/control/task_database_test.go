package control

import (
	"context"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/pkg/execution"
)

func TestTaskComponentsQueueOnOneOwnedPool(t *testing.T) {
	manager, _ := execution.New("runtime", "boot", execution.DefaultLimits())
	keys, _ := secrets.NewMemoryKey(make([]byte, 32))
	s, err := Open(Options{Root: t.TempDir(), RuntimeID: "runtime", BootID: "boot", Manager: manager, Keys: keys})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.Tasks.DB() != s.tasksDB || s.tasksDB.Stats().MaxOpenConnections != 1 {
		t.Fatal("Task database is not the shared single-connection pool")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, err := s.tasksDB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	before := s.tasksDB.Stats().WaitCount
	done := make(chan error, 2)
	go func() { done <- s.Mailbox.FinishChat(ctx, "main", "first", "", true) }()
	go func() { done <- s.RunJournal.CheckPolicyCleanup(ctx) }()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for s.tasksDB.Stats().WaitCount < before+2 {
		select {
		case err := <-done:
			t.Fatalf("component bypassed held shared connection: %v", err)
		case <-deadline.C:
			t.Fatal("components did not queue on shared pool")
		case <-time.After(time.Millisecond):
		}
	}
	if err = conn.Close(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		select {
		case err = <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	if err = s.tasksDB.Ping(); err == nil {
		t.Fatal("Control did not close owned database")
	}
}
