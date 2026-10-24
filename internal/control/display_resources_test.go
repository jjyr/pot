package control

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jjyr/pot/internal/assistant"
	"github.com/jjyr/pot/internal/resources"
)

func TestInternalResourceRegistrationSnapshotAndOwnership(t *testing.T) {
	t.Parallel()
	s, _ := configurationService(t)
	ctx := context.Background()
	a, err := s.CreateAssistant(ctx, CreateAssistant{RequestID: "resources", Name: "Resources"})
	if err != nil {
		t.Fatal(err)
	}
	workspace, _ := s.ExecutionWorkspace(a.ID)
	os.WriteFile(filepath.Join(workspace, "demo.html"), []byte(`<button onclick="this.textContent='Done'">Try</button>`), 0600)
	raw, err := s.registerResource(ctx, a.ID, a.ID, "register", false, json.RawMessage(`{"path":"demo.html"}`))
	if err != nil {
		t.Fatal(err)
	}
	var resource resources.Resource
	if json.Unmarshal(raw, &resource) != nil || resource.MIMEType != "text/html" {
		t.Fatal("invalid registered resource", string(raw))
	}
	if resource.Name != "demo.html" || resource.Download.Path != "demo.html" {
		t.Fatal("default display or download name changed")
	}
	for i, label := range []string{"Pot 通信记录 · 修订 02（A/B 局部对照）", `A\B`, strings.Repeat("中文", 200), strings.Repeat("long/名称", 2000)} {
		args, _ := json.Marshal(map[string]string{"path": "demo.html", "name": label})
		registered, err := s.registerResource(ctx, a.ID, a.ID, fmt.Sprint("label-", i), false, args)
		if err != nil {
			t.Fatal("display label rejected", err)
		}
		var r resources.Resource
		if err := json.Unmarshal(registered, &r); err != nil || r.Name != label {
			t.Fatal("display label changed", err)
		}

	}
	os.Remove(filepath.Join(workspace, "demo.html"))
	retried, err := s.registerResource(ctx, a.ID, a.ID, "register", false, json.RawMessage(`{"path":"demo.html"}`))
	if err != nil || string(raw) != string(retried) {
		t.Fatal("registration retry read deleted source", err)
	}
	if _, err = s.Resources.Get(ctx, "other", resource.ID); err == nil {
		t.Fatal("foreign resource displayed")
	}
	for _, input := range []string{`{"path":"../outside"}`, `{"path":"/etc/passwd"}`, `{"path":"missing"}`, `{"path":"demo.html","assets":["../outside"]}`} {
		if _, err = s.registerResource(ctx, a.ID, a.ID, input, false, json.RawMessage(input)); err == nil {
			t.Fatal("invalid source accepted", input)
		}
	}
	if _, err = s.registerResource(ctx, a.ID, "task", "worker", true, json.RawMessage(`{"path":"demo.html","task_id":"other"}`)); err == nil {
		t.Fatal("worker imported another task")
	}
}

func TestMarkdownResourceRegistrationRetainsWorkspaceAndBundleBoundaries(t *testing.T) {
	t.Parallel()
	s, _ := configurationService(t)
	ctx := context.Background()
	a, err := s.CreateAssistant(ctx, CreateAssistant{RequestID: "markdown-boundaries", Name: "Resources"})
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := s.ExecutionWorkspace(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{"review.md": "# Review\n\n![Missing](missing.png)\n", "asset.txt": "declared asset"} {
		if err := os.WriteFile(filepath.Join(workspace, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("# Outside workspace\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(workspace, "escape.md")); err != nil {
		t.Fatal(err)
	}
	for index, input := range []string{
		`{"path":"review.md","assets":["asset.txt"]}`,
		`{"path":"review.md","assets":["../outside.md"]}`,
		`{"path":"escape.md"}`,
		`{"path":"../outside.md"}`,
	} {
		if _, err := s.registerResource(ctx, a.ID, a.ID, fmt.Sprint("boundary-", index), false, json.RawMessage(input)); err == nil {
			t.Fatal("Markdown bypassed bundle or workspace boundary", input)
		}
	}
	if _, err := s.registerResource(ctx, a.ID, a.ID, "single-markdown", false, json.RawMessage(`{"path":"review.md","name":"Misleading.html"}`)); err != nil {
		t.Fatal("single Markdown with a missing relative image must still register", err)
	}
}

func TestResourceToolsRemoved(t *testing.T) {
	s, _ := configurationService(t)
	for _, worker := range []bool{false, true} {
		dispatch := s.productDispatch("main", "turn", 1, worker)
		for _, name := range []string{"register_resource", "present_resource"} {
			if dispatch[name] != nil || strings.Contains(assistant.MainPrompt, name) {
				t.Fatal("obsolete resource tool remains in dispatch or built-in prompt", name)
			}
		}
		for _, tool := range s.productTools(worker) {
			if tool.Name == "register_resource" || tool.Name == "present_resource" {
				t.Fatal("obsolete display tool", tool.Name)
			}
		}
	}
}

func TestDeletedAssistantCannotReadOrRegisterResources(t *testing.T) {
	t.Parallel()
	s, _ := configurationService(t)
	ctx := context.Background()
	a, err := s.CreateAssistant(ctx, CreateAssistant{RequestID: "resource-owner", Name: "Resources"})
	if err != nil {
		t.Fatal(err)
	}
	workspace, _ := s.ExecutionWorkspace(a.ID)
	if err := os.WriteFile(filepath.Join(workspace, "report.txt"), []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	args := json.RawMessage(`{"path":"report.txt"}`)
	raw, err := s.registerResource(ctx, a.ID, a.ID, "original", false, args)
	if err != nil {
		t.Fatal(err)
	}
	var resource resources.Resource
	if err := json.Unmarshal(raw, &resource); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAssistant(ctx, a.ID, a.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Resources.Get(ctx, a.ID, resource.ID); err == nil {
		t.Fatal("retired owner read resource metadata")
	}
	if _, err := s.Resources.Chunk(ctx, a.ID, resource.ID, "", 0); err == nil {
		t.Fatal("retired owner read resource bytes")
	}
	if _, err := s.registerResource(ctx, a.ID, a.ID, "original", false, args); err == nil {
		t.Fatal("registration receipt revived retired owner")
	}
}
