package control

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/pkg/core"
	"path/filepath"
	"testing"
)

func operation(call string) Operation {
	return Operation{Owner: "assistant", Turn: "turn", Generation: 1, CallID: call, Target: core.HostTarget("machine"), Tool: "exec_command", Args: json.RawMessage(`{"cmd":"printf hello"}`)}
}
func TestReceiptDuplicateBindingRestartFence(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "operations.sqlite")
	o, err := OpenOperations(path)
	if err != nil {
		t.Fatal(err)
	}
	first, claimed, err := o.Begin(ctx, operation("1"))
	if err != nil || !claimed {
		t.Fatal(first, claimed, err)
	}
	if _, _, err = o.Begin(ctx, operation("1")); !errors.Is(err, ErrOperationUnknown) {
		t.Fatal(err)
	}
	result := json.RawMessage(`{"output":"hello"}`)
	if err = o.Complete(ctx, first.ID, result, nil); err != nil {
		t.Fatal(err)
	}
	duplicate, claimed, err := o.Begin(ctx, operation("1"))
	if err != nil || claimed || string(duplicate.Result) != string(result) {
		t.Fatal(duplicate, claimed, err)
	}
	changed := operation("1")
	changed.Target = core.SandboxTarget("machine", "shared")
	if _, _, err = o.Begin(ctx, changed); err == nil {
		t.Fatal("rebound operation")
	}
	if _, _, err = o.Begin(ctx, operation("2")); err != nil {
		t.Fatal(err)
	}
	o.Close()
	o, err = OpenOperations(path)
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	if _, _, err = o.Begin(ctx, operation("2")); !errors.Is(err, ErrOperationUnknown) {
		t.Fatal("replayed crashed operation", err)
	}
	if _, _, err = o.Begin(ctx, operation("3")); !errors.Is(err, ErrOperationUnknown) {
		t.Fatal("bypassed unknown result using new call", err)
	}
}
