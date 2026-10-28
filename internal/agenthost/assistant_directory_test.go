package agenthost

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jjyr/pot/internal/control"
	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/internal/transport"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/execution"
	"github.com/jjyr/pot/pkg/protocol"
)

// Fault only the post-delete directory response, after the real receiver has
// committed its retirement. No fixture execution worker or model is started.

func restartDirectoryControl(t *testing.T, app *transport.Application, owner string) {
	t.Helper()
	root := filepath.Dir(filepath.Dir(app.Control.Registry.Directory(owner)))
	if err := app.Control.Close(); err != nil {
		t.Fatal(err)
	}
	boot := "directory-restart"
	manager, err := execution.New(app.RuntimeID, boot, execution.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	// profileApplication uses this isolated, deterministic in-memory test key.
	keys, err := secrets.NewMemoryKey(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := control.Open(control.Options{Root: root, RuntimeID: app.RuntimeID, BootID: boot, Keys: keys, Manager: manager})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reopened.Close() })
	app.Control, app.BootID = reopened, boot
}

func browserDeleteAssistant(t *testing.T, handler http.Handler, cookie *http.Cookie, owner string, revision uint64) *httptest.ResponseRecorder {
	t.Helper()
	body := strings.NewReader(`{"revision":` + strconv.FormatUint(revision, 10) + `}`)
	r := httptest.NewRequest("POST", "http://localhost/api/v1/assistants/"+url.PathEscape(owner)+"/delete?assistant_id="+url.QueryEscape(owner)+"&space_id=local", body)
	r.AddCookie(cookie)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

// Exercise the actual product-tool query and the browser APIs that supply
// Chat/Task completions and local/linked UI directory projections. Capabilities
// may select different views, but cannot supply additional identities.
func assertBrowserAssistantDirectory(t *testing.T, app *transport.Application, handler http.Handler, cookie *http.Cookie, owner string, expected []protocol.AgentRef, offline map[string]bool) []protocol.Completion {
	t.Helper()
	read := func(path string, target any) {
		t.Helper()
		separator := "?"
		if strings.Contains(path, "?") {
			separator = "&"
		}
		r := httptest.NewRequest("GET", "http://localhost"+path+separator+"assistant_id="+url.QueryEscape(owner)+"&space_id=local", nil)
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), target) != nil {
			t.Fatal("directory browser read failed", path, w.Code)
		}
	}
	want, online := map[protocol.AgentRef]bool{}, map[protocol.AgentRef]bool{}
	for _, ref := range expected {
		want[ref] = true
		if !offline[ref.NodeID] {
			online[ref] = true
		}
	}
	self := agentlink.AssistantTarget(app.RuntimeID, owner)
	listed, err := app.Control.ListAssistants(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	tool := map[protocol.AgentRef]bool{}
	for _, assistant := range listed {
		if assistant.Address != self {
			tool[assistant.Address] = true
		}
	}
	if !maps.Equal(tool, online) {
		t.Fatal("product tool address catalogue differs", tool, online)
	}
	var completions struct{ Items []protocol.Completion }
	read("/api/v1/completions?q=%40", &completions)
	mentions := map[protocol.AgentRef]bool{}
	for _, item := range completions.Items {
		parts := strings.SplitN(strings.TrimPrefix(item.Token, "@assistant:"), "/", 2)
		if len(parts) != 2 || item.Offline != offline[parts[0]] {
			t.Fatal("completion lost its address or offline state", item.Token)
		}
		mentions[agentlink.AssistantTarget(parts[0], parts[1])] = true
	}
	if !maps.Equal(mentions, want) {
		t.Fatal("completion address catalogue differs", mentions, want)
	}
	var locals []struct{ ID string }
	read("/api/v1/assistants", &locals)
	ui := map[protocol.AgentRef]bool{}
	for _, assistant := range locals {
		ref := agentlink.AssistantTarget(app.RuntimeID, assistant.ID)
		if ref != self {
			ui[ref] = true
		}
	}
	var links struct {
		Items []struct {
			Assistants []struct{ Address protocol.AgentRef }
			Directory  *agentlink.Directory
		}
	}
	read("/api/v1/agent-links", &links)
	for _, link := range links.Items {
		projected, saved := map[protocol.AgentRef]bool{}, map[protocol.AgentRef]bool{}
		for _, assistant := range link.Assistants {
			projected[assistant.Address] = true
			ui[assistant.Address] = true
		}
		if link.Directory != nil {
			for _, assistant := range link.Directory.Assistants() {
				saved[assistant.Address] = true
			}
		}
		if !maps.Equal(projected, saved) {
			t.Fatal("UI projection differs from the saved receiver directory", projected, saved)
		}
	}
	if !maps.Equal(ui, want) {
		t.Fatal("UI address catalogue differs", ui, want)
	}
	return completions.Items
}
