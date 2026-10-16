package transport

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jjyr/pot/internal/control"
	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/internal/usage"
	"github.com/jjyr/pot/internal/webui"
	"github.com/jjyr/pot/pkg/execution"
)

func TestRecoveryOwnerAcceptsOnlyIdentityAndIntent(t *testing.T) {
	manager, _ := execution.New("local", "boot", execution.DefaultLimits())
	keys, _ := secrets.NewMemoryKey(make([]byte, 32))
	service, err := control.Open(control.Options{Root: t.TempDir(), RuntimeID: "local", BootID: "boot", Manager: manager, Keys: keys})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	app := Application{Control: service, RuntimeID: "local", BootID: "boot"}
	ctx := context.Background()
	_, err = app.Call(ctx, Request{Method: "POST", Path: "/api/v1/runtime/recovery", Body: json.RawMessage(`{"action":"reconcile","control_id":"local","task_id":"task","run_id":"run","cleanup_confirmed":true}`)})
	if err == nil || !strings.Contains(err.Error(), "cleanup_confirmed") {
		t.Fatal("caller supplied cleanup accepted", err)
	}
	_, err = app.Call(ctx, Request{Method: "POST", Path: "/api/v1/runtime/recovery", Body: json.RawMessage(`{"action":"inspect","control_id":"local","task_id":"task","run_id":"run"}`)})
	if err == nil || !strings.Contains(err.Error(), "original run not found") {
		t.Fatal("owner recovery route unavailable", err)
	}
	_, err = app.Call(ctx, Request{Method: "POST", Path: "/api/v1/turn/recovery", Body: json.RawMessage(`{"action":"reconcile","user_message_id":"original","cleanup_confirmed":true}`)})
	if err == nil || !strings.Contains(err.Error(), "cleanup_confirmed") {
		t.Fatal("Main caller supplied cleanup accepted", err)
	}
	for _, path := range []string{"/api/v1/turn/recovery", "/api/v1/runtime/recovery", "/api/v1/tasks/task/recovery"} {
		if webui.Allowed("POST", path) {
			t.Fatal("private recovery published to browser", path)
		}
	}
}

func TestAllowanceResumeRequiresDisplayedOriginalTurnAndWaitToken(t *testing.T) {
	manager, _ := execution.New("local", "boot", execution.DefaultLimits())
	keys, _ := secrets.NewMemoryKey(make([]byte, 32))
	service, err := control.Open(control.Options{Root: t.TempDir(), RuntimeID: "local", BootID: "boot", Manager: manager, Keys: keys})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	ctx := context.Background()
	if _, err = service.Registry.Create(ctx, "create", "Pot"); err != nil {
		t.Fatal(err)
	}
	if err = service.Usage.Wait(ctx, usage.WaitRequest{Scope: "main", Turn: "new-turn", AtMS: 100, Required: 1}); err != nil {
		t.Fatal(err)
	}
	app := Application{Control: service, RuntimeID: "local", BootID: "boot"}
	for _, body := range []string{`{"request_id":"resume"}`, `{"request_id":"resume","turn":"new-turn"}`, `{"request_id":"resume","turn":"old-turn","since":99}`} {
		if _, err = app.Call(ctx, Request{Method: "POST", Path: "/api/v1/usage/resume", Body: json.RawMessage(body)}); err == nil {
			t.Fatal("stale/unbound allowance action admitted", body)
		}
	}
	waiting, err := service.Usage.Waiting(ctx, "main")
	if err != nil || waiting == nil || waiting.Released {
		t.Fatal(waiting, err)
	}
}
