//go:build darwin || linux

package pluginhost

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jjyr/pot/pkg/plugin"
)

func TestGitHubReadToolInstalledSchemaThroughMCP(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/notifications" || r.URL.Query().Get("per_page") != "30" {
			t.Error(r.Method, r.URL)
		}
		json.NewEncoder(w).Encode([]any{map[string]any{"id": "123", "reason": "mention", "updated_at": "2026-01-01T00:00:00Z", "repository": map[string]any{"full_name": "me/repo"}, "subject": map[string]any{"type": "Issue", "title": "Fixture", "url": "https://api.github.com/repos/me/repo/issues/1"}}})
	}))
	defer api.Close()
	root, _ := filepath.Abs("../../plugins/github")
	p, err := plugin.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	files := append([]string{"plugin.json"}, p.Manifest.Contributes.MCP.Files...)
	files = append(files, p.Manifest.Contributes.Background.Files...)
	for _, name := range files {
		raw, e := os.ReadFile(filepath.Join(root, name))
		if e != nil {
			t.Fatal(e)
		}
		if name == "read-notifications.mjs" {
			raw = []byte(strings.ReplaceAll(string(raw), "https://api.github.com", api.URL))
		}
		os.WriteFile(filepath.Join(dir, name), raw, 0600)
	}
	p, err = plugin.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	worker, _ := os.Executable()
	tools, result, err := RunMCP(context.Background(), "node", p, map[string]string{"GH_TOKEN": "fake-token"}, []string{worker}, "read_notifications", json.RawMessage(`{"per_page":30}`), func(context.Context, string, json.RawMessage) (any, error) {
		t.Fatal("independent read accessed host store")
		return nil, nil
	})
	raw, _ := json.Marshal(result.StructuredContent)
	if err != nil || result.IsError || len(tools) != 5 || !strings.Contains(string(raw), `"thread_id":"123"`) {
		t.Fatal(tools, result, err)
	}
}
