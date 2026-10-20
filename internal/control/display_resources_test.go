package control

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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
