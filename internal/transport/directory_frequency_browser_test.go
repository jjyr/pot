package transport

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/control"
	"github.com/jjyr/pot/internal/tasks"
	"github.com/jjyr/pot/internal/webui"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
	webassets "github.com/jjyr/pot/web"
)

// Opt-in real Control, journal, authenticated gateway, WS and product assets.
// Only fixture state is modified, and model responses come from loopback.
func TestDirectoryFrequencyBrowserFixture(t *testing.T) {
	file := os.Getenv("POT_DIRECTORY_FREQUENCY_URL_FILE")
	if file == "" {
		t.Skip("opt-in directory frequency browser fixture")
	}
	app := historyApplication(t, 80)
	owner := localAssistantID(t, app.Control)
	liveProvider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for i := 0; i < 200; i++ {
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"live-%02d \"}}]}\n\n", i)
			w.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
				return
			case <-time.After(80 * time.Millisecond):
			}
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer liveProvider.Close()
	_, err := app.Control.Profiles.Save(t.Context(), control.SaveProfile{RequestID: "live-profile", ProfileID: "frequency-live", Name: "Synthetic long local response", Model: "gpt-4.1", Endpoint: liveProvider.URL + "/v1", Protocol: "openai_chat", APIKey: "synthetic", MaxOutputTokens: 1000})
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	other, err := app.Control.Registry.Create(t.Context(), "frequency-other", "Other synthetic assistant")
	if err != nil {
		t.Fatal(err)
	}
	config, err := app.Control.Profiles.Resolve("fixture")
	if err != nil {
		t.Fatal(err)
	}
	launch := tasks.Launch{ControlID: app.RuntimeID, RuntimeID: app.RuntimeID, AssistantID: other.ID, TaskID: "frequency-worker", RunID: "frequency-run", Generation: 1, ExecutionTarget: core.RuntimeTarget(app.RuntimeID), Config: config, Plugins: []string{}, Text: "Synthetic journal output"}
	if _, err = app.Control.RunJournal.Accept(t.Context(), launch); err != nil {
		t.Fatal(err)
	}
	if _, err = app.Control.RunJournal.Claim(t.Context(), launch.ControlID, launch.TaskID, launch.RunID); err != nil {
		t.Fatal(err)
	}
	var mu, emitMu sync.Mutex
	trace := []map[string]any{}
	log := func(kind string, fields map[string]any) {
		mu.Lock()
		defer mu.Unlock()
		fields["kind"] = kind
		fields["at"] = time.Now().UnixMilli()
		trace = append(trace, fields)
	}
	applicationHandler := Handler(app, "synthetic-frequency-only")
	var gateMu sync.Mutex
	var directoryGate chan struct{}
	directoryHeld := 0
	t.Cleanup(func() {
		gateMu.Lock()
		gate := directoryGate
		directoryGate = nil
		gateMu.Unlock()
		if gate != nil {
			close(gate)
		}
	})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log("http-start", map[string]any{"path": r.URL.Path, "query": r.URL.RawQuery, "method": r.Method})
		defer log("http-end", map[string]any{"path": r.URL.Path, "query": r.URL.RawQuery, "method": r.Method})
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/assistants" {
			gateMu.Lock()
			gate := directoryGate
			if gate != nil {
				directoryHeld++
			}
			gateMu.Unlock()
			if gate != nil {
				select {
				case <-gate:
				case <-r.Context().Done():
					gateMu.Lock()
					directoryHeld--
					gateMu.Unlock()
					return
				}
				gateMu.Lock()
				directoryHeld--
				gateMu.Unlock()
			}
			select {
			case <-time.After(80 * time.Millisecond):
			case <-r.Context().Done():
				return
			}
		}
		applicationHandler.ServeHTTP(w, r)
	}))
	defer upstream.Close()
	defer func() {
		gateMu.Lock()
		if directoryGate != nil {
			close(directoryGate)
			directoryGate = nil
		}
		gateMu.Unlock()
	}()
	done := make(chan struct{}, 1)
	var gateway *webui.Server
	var gatewayConfig webui.Config
	var sequence uint64
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/fixture/stop":
			select {
			case done <- struct{}{}:
			default:
			}
			return
		case "/fixture/login":
			r = r.Clone(r.Context())
			r.Method = "POST"
			r.URL.Path = "/access/token"
			r.Header.Set("Authorization", "Bearer "+gatewayConfig.Token)
			gateway.ServeHTTP(w, r)
			return
		case "/fixture/state":
			mu.Lock()
			defer mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"owner": owner, "trace": trace})
			return
		case "/fixture/task/start":
			profile := "frequency-live"
			info, err := app.Control.CreateTask(r.Context(), owner, protocol.CreateTask{RequestID: fmt.Sprintf("live-%d", time.Now().UnixNano()), Text: "Synthetic frequency Task\nReturn local fixture text", Profile: &profile, Workspace: protocol.TaskWorkspace{Kind: "directory", ProjectDir: workspace}})
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			log("task-created", map[string]any{"id": info.ID})
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"owner": owner, "task": info})
			return
		case "/fixture/task/state":
			record, err := app.Control.Tasks.Get(r.Context(), owner, r.URL.Query().Get("id"))
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(record)
			return
		case "/fixture/directory-gate":
			var body struct {
				Hold bool `json:"hold"`
			}
			if r.Method == http.MethodPost {
				if json.NewDecoder(r.Body).Decode(&body) != nil {
					http.Error(w, "invalid gate request", 400)
					return
				}
			}
			gateMu.Lock()
			if r.Method == http.MethodPost {
				if body.Hold {
					if directoryGate == nil {
						directoryGate = make(chan struct{})
					}
				} else if directoryGate != nil {
					close(directoryGate)
					directoryGate = nil
				}
			}
			held := directoryHeld
			holding := directoryGate != nil
			gateMu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"held": held, "holding": holding})
			return
		case "/fixture/emit":
			emitMu.Lock()
			defer emitMu.Unlock()
			var body struct {
				Kind     string `json:"kind"`
				Count    int    `json:"count"`
				Interval int    `json:"interval_ms"`
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil || body.Count < 1 || body.Count > 80 || body.Interval < 0 || body.Interval > 500 {
				http.Error(w, "invalid synthetic pulse", 400)
				return
			}
			for i := 0; i < body.Count; i++ {
				log("emit", map[string]any{"event": body.Kind, "index": i})
				switch body.Kind {
				case "transcript":
					sequence++
					err := app.Control.RunJournal.AppendEvent(r.Context(), launch.ControlID, launch.TaskID, launch.RunID, core.AgentEvent{Generation: 1, TurnID: launch.RunID, Seq: sequence, Update: core.Update{Type: "text_delta", Text: fmt.Sprintf("synthetic-%d ", sequence)}})
					if err != nil {
						http.Error(w, err.Error(), 500)
						return
					}
				case "tasks":
					app.Control.Tasks.Notify()
				case "permissions":
					app.Control.Permissions.Changes.Notify("permissions")
				case "usage", "assistants":
					app.Control.Changes.Notify(body.Kind)
				default:
					http.Error(w, "unknown synthetic pulse", 400)
					return
				}
				if body.Interval > 0 {
					select {
					case <-r.Context().Done():
						return
					case <-time.After(time.Duration(body.Interval) * time.Millisecond):
					}
				}
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{}`)
			return
		}
		gateway.ServeHTTP(w, r)
	}))
	defer front.Close()
	gatewayConfig = webui.NewConfig()
	gateway, err = webui.New(webui.Options{Origin: front.URL, Config: gatewayConfig, Upstream: webui.Upstream{URL: upstream.URL, Credential: "synthetic-frequency-only", Client: upstream.Client()}, Assets: webassets.FS()})
	if err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	if err = os.WriteFile(file, []byte(front.URL), 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(12 * time.Minute):
		t.Fatal("fixture expired")
	}
}
