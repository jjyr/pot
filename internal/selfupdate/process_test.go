package selfupdate

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestExitWatchChild(t *testing.T) {
	if os.Getenv("POT_TEST_EXIT_WATCH") != "1" {
		return
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
}
func TestProcessWatchConfirmsExit(t *testing.T) {
	child := exec.Command(os.Args[0], "-test.run=^TestExitWatchChild$")
	child.Env = append(os.Environ(), "POT_TEST_EXIT_WATCH=1")
	input, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { input.Close(); _ = child.Wait() }()
	watch, err := watchProcess(child.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	defer watch.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err = watch.Wait(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("live child satisfied exit watch", err)
	}
	input.Close()
	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	if err = watch.Wait(ctx2); err != nil {
		t.Fatal(err)
	}
}
