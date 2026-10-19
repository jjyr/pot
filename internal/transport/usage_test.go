package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jjyr/pot/internal/control"
	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/internal/usage"
	"github.com/jjyr/pot/internal/webui"
	"github.com/jjyr/pot/pkg/execution"
	"github.com/jjyr/pot/pkg/protocol"
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
			var summary usage.Summary
			if err := json.Unmarshal(data, &summary); err != nil || summary.Days != 365 || len(summary.Buckets) != 365 {
				t.Fatal("summary must default to a year of daily slots", string(data), err)
			}
			if value["total"] == nil || value["buckets"] == nil || value["agents"] == nil || value["models"] == nil || value["requests"] != nil || value["agent_models"] != nil || value["tools"] != nil || value["compactions"] != nil {
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

func TestBrowserReadRoutesReachApplication(t *testing.T) {
	manager, _ := execution.New("local", "boot", execution.DefaultLimits())
	keys, _ := secrets.NewMemoryKey(make([]byte, 32))
	service, err := control.Open(control.Options{Root: t.TempDir(), RuntimeID: "local", BootID: "boot", Manager: manager, Keys: keys})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	ctx := context.Background()
	owner, err := service.Registry.Create(ctx, "create", "Pot")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Tasks.DB().Exec(`INSERT INTO task_outcomes(assistant_id,task_id,title,phase,updated_at_ms) VALUES(?, 'own-task','Own task','complete',1),('other','other-task','Other task','failed',2)`, owner.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Usage.Reserve(ctx, usage.ReserveRequest{ID: "request", Scope: "main", Turn: "turn", AtMS: time.Now().UnixMilli(), Model: "fixture", InputEstimate: 10, MaxOutput: 20}); err != nil {
		t.Fatal(err)
	}
	app := &Application{Control: service, RuntimeID: "local", BootID: "boot", About: func(context.Context) (any, error) {
		return map[string]string{"version": "fixture"}, nil
	}}
	upstream := httptest.NewServer(Handler(app, "control-secret"))
	defer upstream.Close()
	config := webui.NewConfig()
	browser, err := webui.New(webui.Options{Origin: "http://localhost", Config: config, Upstream: webui.Upstream{URL: upstream.URL, Credential: "control-secret", Client: upstream.Client()}, Assets: fstest.MapFS{}})
	if err != nil {
		t.Fatal(err)
	}
	defer browser.Close()
	login := httptest.NewRequest("POST", "http://localhost/access/token", nil)
	login.Header.Set("Authorization", "Bearer "+config.Token)
	response := httptest.NewRecorder()
	browser.ServeHTTP(response, login)
	if response.Code != http.StatusOK {
		t.Fatal("browser login failed", response.Code)
	}
	cookie := response.Result().Cookies()[0]
	for _, path := range []string{
		"/api/v1/runtime", "/api/v1/control", "/api/v1/spaces", "/api/v1/assistants",
		"/api/v1/providers", "/api/v1/agent", "/api/v1/agent/routing", "/api/v1/instructions", "/api/v1/about",
		"/api/v1/integrations", "/api/v1/agent/diagnostics",
		"/api/v1/snapshot", "/api/v1/permissions", "/api/v1/machines", "/api/v1/schedules",
		"/api/v1/tasks/query", "/api/v1/tasks/catalog", "/api/v1/tasks/outcomes", "/api/v1/tasks/outcomes?after=0", "/api/v1/usage/providers",
		"/api/v1/usage/summary?scope=main&timezone=UTC",
		"/api/v1/usage/models?scope=main&timezone=UTC&offset=0",
		"/api/v1/usage/requests?scope=main&timezone=UTC&offset=0",
		"/api/v1/usage/tools?scope=main&timezone=UTC&offset=0",
		"/api/v1/usage/compactions?scope=main&timezone=UTC&offset=0",
		"/api/v1/usage/models?days=365&timezone=Asia%2FShanghai&offset=20",
	} {
		t.Run(path, func(t *testing.T) {
			r := httptest.NewRequest("GET", "http://localhost"+path, nil)
			r.AddCookie(cookie)
			w := httptest.NewRecorder()
			browser.ServeHTTP(w, r)
			if w.Code != http.StatusOK || !json.Valid(w.Body.Bytes()) {
				t.Fatalf("browser/application route mismatch: %d %s", w.Code, w.Body.String())
			}
			// Check persisted data and the policy consumed by the frontend editor.
			if path == "/api/v1/usage/requests?scope=main&timezone=UTC&offset=0" {
				var page struct{ Items []usage.RequestRecord }
				if err = json.Unmarshal(w.Body.Bytes(), &page); err != nil || len(page.Items) != 1 || page.Items[0].ID != "request" {
					t.Fatal("request page lost original scope", w.Body.String(), err)
				}
			}
			if path == "/api/v1/usage/summary?scope=main&timezone=UTC" {
				var summary usage.Summary
				if err = json.Unmarshal(w.Body.Bytes(), &summary); err != nil || summary.Quota.Policy != usage.DefaultPolicy() {
					t.Fatal("summary must include the policy used by the editor", w.Body.String(), err)
				}
			}
			if path == "/api/v1/tasks/outcomes" || path == "/api/v1/tasks/outcomes?after=0" {
				var page protocol.TaskOutcomePage
				if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || page.Cursor != 1 || page.HasMore {
					t.Fatal("invalid outcome response", page, err)
				}
				if path == "/api/v1/tasks/outcomes" && len(page.Items) != 0 || path != "/api/v1/tasks/outcomes" && (len(page.Items) != 1 || page.Items[0].ID != "own-task") {
					t.Fatal("baseline history or another owner leaked", page)
				}
			}
		})
	}
	for _, path := range []string{"/api/v1/runtime/control", "/api/v1/runtime/resources", "/api/v1/terminals", "/api/v1/usage/policy"} {
		r := httptest.NewRequest("GET", "http://localhost"+path, nil)
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		browser.ServeHTTP(w, r)
		if w.Code != http.StatusNotFound {
			t.Fatal("private or unsupported route published", path, w.Code, w.Body.String())
		}
	}
	for _, query := range []string{"after=", "after=-1", "after=9007199254740992", "after=nope"} {
		if _, err := app.Call(ctx, Request{Method: "GET", Path: "/api/v1/tasks/outcomes?" + query}); err == nil {
			t.Fatal("invalid outcome cursor accepted", query)
		}
	}
}

func TestModelRequestPagesNewestFirst(t *testing.T) {
	manager, _ := execution.New("local", "boot", execution.DefaultLimits())
	keys, _ := secrets.NewMemoryKey(make([]byte, 32))
	service, err := control.Open(control.Options{Root: t.TempDir(), RuntimeID: "local", BootID: "boot", Manager: manager, Keys: keys})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	ctx := context.Background()
	at := time.Now().UnixMilli()
	reserve := func(id, scope string, at int64) {
		t.Helper()
		if _, err := service.Usage.Reserve(ctx, usage.ReserveRequest{ID: id, Scope: scope, Turn: "turn", AtMS: at, Model: "fixture", InputEstimate: 10, MaxOutput: 20}); err != nil {
			t.Fatal(err)
		}
	}
	// IDs deliberately disagree with time order. Equal timestamps span two pages.
	reserve("z-oldest", "main", at-1)
	for i := 0; i < 22; i++ {
		reserve(fmt.Sprintf("tie-%02d", i), "main", at)
	}
	reserve("a-newest", "main", at+1)
	reserve("other-scope", "task", at+2)
	want := []string{"a-newest"}
	for i := 21; i >= 0; i-- {
		want = append(want, fmt.Sprintf("tie-%02d", i))
	}
	want = append(want, "z-oldest")
	app := Application{Control: service}
	for _, offset := range []int{0, 20, 40} {
		result, err := app.Call(ctx, Request{Method: "GET", Path: fmt.Sprintf("/api/v1/usage/requests?scope=main&timezone=UTC&offset=%d", offset)})
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		var page struct {
			Items      []usage.RequestRecord `json:"items"`
			Offset     int                   `json:"offset"`
			NextOffset *int                  `json:"next_offset"`
		}
		if err := json.Unmarshal(data, &page); err != nil {
			t.Fatal(err)
		}
		ids := []string{}
		for _, row := range page.Items {
			ids = append(ids, row.ID)
		}
		if !reflect.DeepEqual(ids, want[min(offset, len(want)):min(offset+20, len(want))]) || page.Offset != offset {
			t.Fatalf("offset %d: unexpected request page %s", offset, data)
		}
		if (page.NextOffset != nil) != (offset == 0) || page.NextOffset != nil && *page.NextOffset != 20 {
			t.Fatalf("offset %d: invalid next offset %s", offset, data)
		}
	}
}
