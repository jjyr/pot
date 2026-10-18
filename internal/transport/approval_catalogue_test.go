package transport

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/control"
	product "github.com/jjyr/pot/internal/runtime"
	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/modelprotocol"
)

func TestAssistantCatalogueCountsPendingApprovalsWithoutCreatingReplies(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	keys, _ := secrets.NewMemoryKey(make([]byte, 32))
	runtime, err := product.Open(ctx, product.Options{Root: filepath.Join(t.TempDir(), "runtime"), DisableMetadataRefresh: true, Control: control.Options{Keys: keys}})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	first, err := runtime.Control.Registry.Create(ctx, "first", "First")
	if err != nil {
		t.Fatal(err)
	}
	second, err := runtime.Control.Registry.Create(ctx, "second", "Second")
	if err != nil {
		t.Fatal(err)
	}
	spec, err := (core.Profile{ID: "fixture", Revision: 1, Name: "Fixture", Provider: "mock", Model: "model", AuthRef: "provider:mock", Params: core.Parameters{MaxOutputTokens: 100, Agent: core.DefaultAgentSettings()}, ToolPolicy: core.ToolPolicy{SummaryReadOnly: true, Unattended: "runtime"}}).Resolve()
	if err != nil {
		t.Fatal(err)
	}
	id := runtime.Root.Identity.RuntimeID
	dispatcher := &control.Dispatcher{RuntimeID: id, Owner: first.ID, Turn: "review", Generation: 1, DefaultTarget: core.RuntimeTarget(id), Spec: spec, Permissions: runtime.Control.Permissions, Operations: runtime.Control.Operations}
	ready, done := make(chan struct{}, 1), make(chan error, 1)
	go func() {
		_, err := dispatcher.Dispatch(ctx, core.ExecutionContext{Owner: first.ID, Role: core.Main, Workspace: t.TempDir()}, modelprotocol.ToolCall{ID: "request", Name: "exec_command", Arguments: json.RawMessage(`{"cmd":"printf fixture","login":false,"request_unsandboxed":true,"justification":"Synthetic approval catalogue check"}`)}, func(update core.Update) error {
			if update.Phase == "waiting_approval" {
				ready <- struct{}{}
			}
			return nil
		})
		done <- err
	}()
	select {
	case <-ready:
	case err := <-done:
		t.Fatal("approval request failed", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	app := &Application{Control: runtime.Control, RuntimeID: id, BootID: runtime.BootID}
	check := func(expected int) {
		t.Helper()
		value, err := app.Call(ctx, Request{Method: "GET", Path: "/api/v1/assistants?assistant_id=" + second.ID})
		if err != nil {
			t.Fatal(err)
		}
		counts := map[string]int{}
		for _, item := range value.([]map[string]any) {
			counts[item["id"].(string)] = item["pending_approvals"].(int)
			if item["reply_count"] != uint64(0) {
				t.Fatal("approval notification created a reply", item)
			}
		}
		if counts[first.ID] != expected || counts[second.ID] != 0 {
			t.Fatal("approval counts leaked across assistants", counts)
		}
	}
	check(1)
	pending := runtime.Control.Permissions.Pending(first.ID)
	if len(pending) != 1 {
		t.Fatal(pending)
	}
	if err = runtime.Control.Permissions.Decide(first.ID, pending[0].ID, "deny"); err != nil {
		t.Fatal(err)
	}
	if err = <-done; !errors.Is(err, control.ErrHostDenied) {
		t.Fatal(err)
	}
	check(0)
}
