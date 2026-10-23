package control

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jjyr/pot/internal/resources"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/modelprotocol"
)

func TestResourceToolsSnapshotActualFilesAndFenceOwnership(t *testing.T) {
	s, _ := configurationService(t)
	ctx := context.Background()
	a, err := s.CreateAssistant(ctx, CreateAssistant{RequestID: "resources", Name: "Resources"})
	if err != nil {
		t.Fatal(err)
	}
	workspace, _ := s.ExecutionWorkspace(a.ID)
	os.WriteFile(filepath.Join(workspace, "demo.html"), []byte(`<button onclick="this.textContent='Done'">Try</button>`), 0600)
	c, _ := s.Conversation(ctx, a.ID)
	thread, _ := c.Thread(ctx)
	dispatch := s.productDispatch(a.ID, "turn", thread.Generation, false)
	call := modelprotocol.ToolCall{ID: "register", Name: "register_resource", Arguments: json.RawMessage(`{"path":"demo.html"}`)}
	raw, err := dispatch[call.Name](ctx, core.ExecutionContext{Owner: a.ID}, call, nil)
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
		args, _ = json.Marshal(map[string]string{"resource_id": r.ID, "mode": "preview"})
		shown, err := s.presentResource(ctx, a.ID, args)
		if err != nil {
			t.Fatal(err)
		}
		var result struct {
			Presentation resources.Presentation `json:"presentation"`
		}
		if err := json.Unmarshal(shown, &result); err != nil || result.Presentation.Resource.Name != label {
			t.Fatal("presentation label changed", err)
		}
		if err := result.Presentation.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	os.Remove(filepath.Join(workspace, "demo.html"))
	retried, err := dispatch[call.Name](ctx, core.ExecutionContext{Owner: a.ID}, call, nil)
	if err != nil || string(raw) != string(retried) {
		t.Fatal("registration retry read deleted source", err)
	}
	args, _ := json.Marshal(map[string]string{"resource_id": resource.ID, "mode": "preview"})
	result, err := s.presentResource(ctx, a.ID, args)
	if err != nil {
		t.Fatal(err)
	}
	var presentation struct {
		Presentation resources.Presentation `json:"presentation"`
	}
	json.Unmarshal(result, &presentation)
	if presentation.Presentation.Resource.ID != resource.ID {
		t.Fatal("missing explicit presentation")
	}
	if _, err = s.presentResource(ctx, "other", args); err == nil {
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

func TestRegisterResourceNameSchemaDescribesDisplayLabel(t *testing.T) {
	for _, tool := range resourceTools() {
		if tool.Name != "register_resource" {
			continue
		}
		var schema struct {
			Properties map[string]map[string]any `json:"properties"`
		}
		if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
			t.Fatal(err)
		}
		name := schema.Properties["name"]
		if name["type"] != "string" || name["maxLength"] != nil || name["pattern"] != nil || !strings.Contains(name["description"].(string), "display label") {
			t.Fatal("name schema restricts display labels", name)
		}
	}
}

func TestDeletedAssistantCannotReadOrRegisterResources(t *testing.T) {
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
