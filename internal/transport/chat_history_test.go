package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/control"
	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/execution"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

func historyApplication(t *testing.T, count int) *Application {
	t.Helper()
	ctx := context.Background()
	var failed atomic.Bool
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		data, _ := json.Marshal(body)
		if strings.Contains(string(data), "fail-once-fixture") && !failed.Swap(true) {
			http.Error(w, "fixture failure", 503)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for i := 0; i < 12; i++ {
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"part-%02d \"}}]}\n\n", i)
			w.(http.Flusher).Flush()
			time.Sleep(50 * time.Millisecond)
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(provider.Close)
	keys, _ := secrets.NewMemoryKey(make([]byte, 32))
	manager, _ := execution.New("history-fixture", "boot", execution.DefaultLimits())
	root := t.TempDir()
	service, err := control.Open(control.Options{Root: root, RuntimeID: "history-fixture", BootID: "boot", Keys: keys, Manager: manager})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { service.Close() })
	if _, err = service.Registry.Create(ctx, "fixture", "History fixture"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "assistants", "main", "conversation.sqlite")
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= count; i++ {
		m := protocol.Message{ID: fmt.Sprintf("m-%04d", i), ConversationID: "main", Role: protocol.User, Text: fmt.Sprintf("Message %04d: synthetic history.\n\nA paragraph to read while loading older messages.", i), Status: protocol.Complete, CreatedAtMS: 1}
		switch i % 8 {
		case 1:
			m.Text += "\n```js\nconst answer = 42;\nconsole.log(answer);\n```"
			m.References = []core.MessageReference{{MessageID: "m-0002", Quote: "Synthetic answer"}}
		case 2, 4, 6, 0:
			m.Role = protocol.Assistant
			m.Text = fmt.Sprintf("Synthetic answer %04d.\n\n**Measured Markdown** with a [link](https://example.com) and more text.\n\n- First point\n- Second point", i)
			if i%8 == 2 {
				m.ReplyTo = fmt.Sprintf("m-%04d", i-1)
			}
		case 3:
			m.Role = protocol.Tool
			m.Text = ""
			m.Tool = &protocol.ToolCall{ID: fmt.Sprintf("tool-%d", i), TurnID: fmt.Sprintf("turn-%d", i/8), Name: "exec_command", Args: json.RawMessage(`{"cmd":"printf fixture"}`), Phase: "completed", StartedAtMS: 1, FinishedAtMS: 2, Result: json.RawMessage(`{"output":"Synthetic tool output"}`)}
		case 5:
			m.Attachments = []core.Attachment{{ID: "fixture-image", Name: "fixture.png", MIMEType: "image/png", Size: 100, Width: 400, Height: 260}, {ID: "fixture-pdf", Name: "fixture.pdf", MIMEType: "application/pdf", Size: 100}}
		case 7:
			m.Role = protocol.Tool
			m.Text = ""
			m.Tool = &protocol.ToolCall{ID: fmt.Sprintf("tool-%d", i), TurnID: fmt.Sprintf("turn-%d", i/8), ParentID: fmt.Sprintf("tool-%d", i-4), Name: "exec_command", Args: json.RawMessage(`{"cmd":"pwd"}`), Phase: "completed", StartedAtMS: 1, FinishedAtMS: 2, Result: json.RawMessage(`{"output":"synthetic directory"}`)}
		}
		if _, err = store.Save(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	store.Close()
	profile, err := service.Profiles.Save(ctx, control.SaveProfile{RequestID: "provider", ProfileID: "fixture", Name: "Local fake model", Model: "gpt-4.1", Endpoint: provider.URL + "/v1", Protocol: "openai_chat", APIKey: "fixture", MaxOutputTokens: 100})
	if err != nil {
		t.Fatal(err)
	}
	spec, err := service.Profiles.ResolveAssistant(control.AssistantSelection{ProfileID: profile.ID, ProfileRevision: profile.Revision, Model: profile.Model})
	if err != nil {
		t.Fatal(err)
	}
	spec.Params.Agent.AutoCompact = false
	spec.Params.Agent.BuiltinTools = []string{}
	spec.Rehash()
	c, err := service.Conversation(ctx, "main")
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Configure(ctx, spec, 0); err != nil {
		t.Fatal(err)
	}
	return &Application{Control: service, RuntimeID: "history-fixture", BootID: "boot", Name: "History fixture"}
}

func TestHistoryHTTPBoundsAndScope(t *testing.T) {
	app := historyApplication(t, 245)
	handler := Handler(app, "private")
	other, err := app.Control.Registry.Create(context.Background(), "other", "Other")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		path, token string
		code        int
	}{
		{"/api/v1/history?assistant_id=main", "wrong", 401},
		{"/api/v1/history?assistant_id=missing", "private", 400},
		{"/api/v1/history?assistant_id=" + other.ID + "&around=m-0002", "private", 404},
		{"/api/v1/history?assistant_id=main&limit=121", "private", 400},
		{"/api/v1/history?assistant_id=main&before=-1", "private", 400},
		{"/api/v1/history?assistant_id=main&before=2&after=1", "private", 400},
		{"/api/v1/history?assistant_id=main&before=9007199254740992", "private", 400},
		{"/api/v1/history?assistant_id=main&around=m-0002", "private", 200},
		{"/api/v1/snapshot?assistant_id=main", "private", 200},
	} {
		t.Run(test.path, func(t *testing.T) {
			r := httptest.NewRequest("GET", test.path, nil)
			r.Header.Set("Authorization", "Bearer "+test.token)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != test.code {
				t.Fatal(w.Code, w.Body.String())
			}
			if strings.Contains(test.path, "/snapshot") && w.Code == 200 {
				var frame protocol.UIFrame
				if err := protocol.DecodeStrict(w.Body.Bytes(), &frame); err != nil || len(frame.Messages) > 113 {
					t.Fatal(err, len(frame.Messages))
				}
			}
		})
	}
}

// Opt-in disposable real HTTP/SSE host for the shared browser fixture. It never
// opens product roots, installs providers or emits native notifications.
func TestHistoryBrowserFixture(t *testing.T) {
	urlFile := os.Getenv("POT_HISTORY_FIXTURE_URL_FILE")
	if urlFile == "" {
		t.Skip("opt-in interactive fixture")
	}
	app := historyApplication(t, 2000)
	handler := Handler(app, "private-fixture")
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	files := http.FileServer(http.Dir(repo))
	done := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/fixture/stop" {
			select {
			case done <- struct{}{}:
			default:
			}
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			r.Header.Set("Authorization", "Bearer private-fixture")
			handler.ServeHTTP(w, r)
			return
		}
		files.ServeHTTP(w, r)
	}))
	defer server.Close()
	if err = os.WriteFile(urlFile, []byte(server.URL), 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(30 * time.Minute):
		t.Fatal("fixture expired")
	}
}
