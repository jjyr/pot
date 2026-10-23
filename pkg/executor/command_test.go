package executor

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestDirectCommandLiteralArgvStdinAndEnvironment(t *testing.T) {
	e, r := fixture(t)
	worker, _ := os.Executable()
	e.options.ProcessWorker = []string{worker}
	t.Setenv("POT_COMMAND_SECRET", "do-not-inherit")
	value, err := e.RunCommand(context.Background(), r, CommandInput{Argv: []string{"/bin/sh", "-c", `read line; printf '%s|%s|%s' "$line" "$1" "${POT_COMMAND_SECRET-unset}"`, "fixture", "$(touch injected); `exit 9`"}, Input: "bounded context\n"})
	if err != nil || value["exit_code"] != 0 || value["stdout"] != "bounded context|$(touch injected); `exit 9`|unset" {
		t.Fatal(value, err)
	}
}
func TestDirectCommandDeadlineAndOutputBounds(t *testing.T) {
	e, r := fixture(t)
	worker, _ := os.Executable()
	e.options.ProcessWorker = []string{worker}
	r.Policy.DefaultOutputBytes = 32
	r.Policy.MaxOutputBytes = 32
	value, err := e.RunCommand(context.Background(), r, CommandInput{Argv: []string{"/bin/sh", "-c", "printf '%0100d' 0; printf '%0100d' 0 >&2"}})
	if err != nil || len(value["stdout"].(string)) > 32 || len(value["stderr"].(string)) > 32 || value["truncated"] != true {
		t.Fatal(value, err)
	}
	e2, r2 := fixture(t)
	e2.options.ProcessWorker = []string{worker}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err = e2.RunCommand(ctx, r2, CommandInput{Argv: []string{"/bin/sh", "-c", "sleep 60 & wait"}})
	if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrCleanupUnconfirmed) {
		t.Fatal(err)
	}
	inventory, err := e2.Inspect()
	if err != nil || inventory["active"] != false {
		t.Fatal(inventory, err)
	}
}
func TestDirectCommandChecksAdmissionAndRejectsCredentialInput(t *testing.T) {
	e, r := fixture(t)
	denied := errors.New("admission denied")
	e.options.Admit = func(uint64) (func(), error) { return nil, denied }
	if _, err := e.RunCommand(context.Background(), r, CommandInput{Argv: []string{"/bin/true"}}); !errors.Is(err, denied) {
		t.Fatal(err)
	}
	r.Environment = map[string]string{"TOKEN": "secret"}
	if _, err := e.RunCommand(context.Background(), r, CommandInput{Argv: []string{"/bin/true"}}); err == nil {
		t.Fatal("credential injection allowed")
	}
	if _, err := e.RunCommand(context.Background(), r, CommandInput{Argv: []string{"/bin/true"}, Input: strings.Repeat("x", 4097)}); err == nil {
		t.Fatal("unbounded stdin")
	}
}
