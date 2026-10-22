package tasks

import (
	"context"
	"github.com/jjyr/pot/pkg/protocol"
	"testing"
	"time"
)

func TestTaskPauseGateBindsOriginalRunAndCancellationWakesIt(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	s, _, _ := fixture(t)
	record, err := s.Create(ctx, "assistant", protocol.CreateTask{RequestID: "create", Text: "work"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.Control(ctx, "assistant", record.Info.ID, "wrong", "pause"); err == nil {
		t.Fatal("different original run paused")
	}
	if err = s.Control(ctx, "assistant", record.Info.ID, record.RunID, "pause"); err != nil {
		t.Fatal(err)
	}
	ready := make(chan error, 1)
	go func() { ready <- s.ReadyGate(ctx, "assistant", record.Info.ID, record.RunID) }()
	select {
	case err := <-ready:
		t.Fatal("paused Task passed boundary", err)
	case <-time.After(40 * time.Millisecond):
	}
	if err = s.Control(ctx, "assistant", record.Info.ID, record.RunID, "resume"); err != nil {
		t.Fatal(err)
	}
	if err = <-ready; err != nil {
		t.Fatal(err)
	}
	if err = s.Control(ctx, "assistant", record.Info.ID, record.RunID, "pause"); err != nil {
		t.Fatal(err)
	}
	go func() { ready <- s.ReadyGate(ctx, "assistant", record.Info.ID, record.RunID) }()
	if err = s.Stop(ctx, "assistant", record.Info.ID, "cancel"); err != nil {
		t.Fatal(err)
	}
	if err = <-ready; err == nil {
		t.Fatal("stopping Task admitted model boundary")
	}
}
