package transport

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jjyr/pot/internal/control"
	"github.com/jjyr/pot/internal/execution"
	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/internal/webui"
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
