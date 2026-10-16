package control

import (
	"context"
	"encoding/json"
	"github.com/jjyr/pot/internal/gateway"
	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/pkg/core"
	"path/filepath"
	"strings"
	"testing"
)

func TestProfilesPinRoutesAndReconcileOriginalResponse(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	keys, _ := secrets.NewMemoryKey(make([]byte, 32))
	registry, err := gateway.Open(filepath.Join(root, "db.sqlite"), keys)
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	path := filepath.Join(root, "profiles.json")
	p, err := OpenProfiles(path, "", registry)
	if err != nil {
		t.Fatal(err)
	}
	settings := core.DefaultAgentSettings()
	settings.Plugins = map[string]core.PluginConfig{}
	request := SaveProfile{RequestID: "request", ProfileID: "profile", Name: "A", Model: "model", Endpoint: "https://api.example.com", Protocol: "openai_chat", APIKey: "private-token", Agent: &settings}
	first, err := p.Save(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	updated := request
	updated.RequestID = "update"
	updated.ExpectedRevision = first.Revision
	updated.Name = "B"
	updated.APIKey = ""
	if _, err = p.Save(ctx, updated); err != nil {
		t.Fatal(err)
	}
	p, err = OpenProfiles(path, "", registry)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := p.Save(ctx, request)
	if err != nil || duplicate.Revision != first.Revision || duplicate.Provider != first.Provider {
		t.Fatal(duplicate, err)
	}
	request.APIKey = "another-key"
	if _, err = p.Save(ctx, request); err == nil {
		t.Fatal("credential mismatch accepted")
	}
	data, _ := json.Marshal(p.Snapshot())
	if strings.Contains(string(data), "private-token") {
		t.Fatal("public credential disclosure")
	}
	original, err := registry.Get(ctx, first.Provider)
	if err != nil {
		t.Fatal(err)
	}
	key, err := registry.Credential(ctx, original)
	if err != nil || string(key) != "private-token" {
		t.Fatal(err)
	}
	clear(key)
}
