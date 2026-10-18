package transport

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/control"
	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/internal/usage"
	"github.com/jjyr/pot/pkg/execution"
)

func TestUsageSummaryAndIndependentListRoutes(t *testing.T) {
	manager, _ := execution.New("local", "boot", execution.DefaultLimits())
	keys, _ := secrets.NewMemoryKey(make([]byte, 32))
	service, err := control.Open(control.Options{Root: t.TempDir(), RuntimeID: "local", BootID: "boot", Manager: manager, Keys: keys})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	ctx := context.Background()
	at := time.Now().UnixMilli()
	if _, err := service.Usage.Reserve(ctx, usage.ReserveRequest{ID: "request", Scope: "main", Turn: "turn", AtMS: at, Model: "fixture", InputEstimate: 10, MaxOutput: 20}); err != nil {
		t.Fatal(err)
	}
	app := Application{Control: service}
	for _, kind := range []string{"summary", "models", "requests", "tools", "compactions"} {
		result, err := app.Call(ctx, Request{Method: "GET", Path: "/api/v1/usage/" + kind + "?scope=main&timezone=UTC&offset=0"})
		if err != nil {
			t.Fatal(kind, err)
		}
		data, _ := json.Marshal(result)
		var value map[string]json.RawMessage
		if err := json.Unmarshal(data, &value); err != nil {
			t.Fatal(err)
		}
		if kind == "summary" {
			if value["total"] == nil || value["buckets"] == nil || value["requests"] != nil || value["agent_models"] != nil || value["tools"] != nil || value["compactions"] != nil {
				t.Fatal("summary must not load detail lists", string(data))
			}
		} else if value["items"] == nil || string(value["items"]) == "null" || value["next_offset"] == nil || value["total"] != nil {
			t.Fatal("list must return only a bounded page", kind, string(data))
		}
	}
	for _, path := range []string{"/api/v1/usage/requests", "/api/v1/usage/tools?scope=main&offset=-1", "/api/v1/usage/models?days=2", "/api/v1/usage/compactions?scope=main&timezone=Invalid"} {
		if _, err := app.Call(ctx, Request{Method: "GET", Path: path}); err == nil {
			t.Fatal("invalid list query accepted", path)
		}
	}
}
