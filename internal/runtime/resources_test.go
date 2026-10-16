package runtime

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/jjyr/pot/internal/control"
	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/pkg/sandbox"
)

func TestAdvancedOwnerResourcesCreateHostWithoutReplayOrImplicitAssistant(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	os.Chmod(root, 0700)
	keys, _ := secrets.NewMemoryKey(make([]byte, 32))
	r, err := Open(ctx, Options{Root: root, SandboxProviders: []sandbox.Provider{}, DisableMetadataRefresh: true, Control: control.Options{Keys: keys}})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	snapshot, err := r.ResourceSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(snapshot)
	var view struct {
		Assistants  []json.RawMessage    `json:"assistants"`
		Definitions []sandbox.Definition `json:"sandbox_definitions"`
	}
	json.Unmarshal(raw, &view)
	if len(view.Assistants) != 0 || len(view.Definitions) != 0 {
		t.Fatal("resource inventory manufactured definitions")
	}
	request := json.RawMessage(`{"action":"create","kind":"host","request_id":"create-host"}`)
	first, err := r.ResourceCall(ctx, "POST", request)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.Control.Permissions.Configure(r.Root.Identity.RuntimeID, control.MachinePolicy{ExecutionMode: "host", HostAccess: "allow"}); err != nil {
		t.Fatal(err)
	}
	again, err := r.ResourceCall(ctx, "POST", request)
	if err != nil {
		t.Fatal(err)
	}
	left, _ := json.Marshal(first)
	right, _ := json.Marshal(again)
	if string(left) != string(right) {
		t.Fatal("owner retry changed original response")
	}
	policy, _, _ := r.Control.Permissions.Policy(r.Root.Identity.RuntimeID)
	if policy.HostAccess != "allow" {
		t.Fatal("old create reset new policy")
	}
	if _, err = r.ResourceCall(ctx, "POST", json.RawMessage(`{"action":"create","kind":"sandbox","request_id":"unsupported","sandbox_id":"explicit","name":"Sandbox","provider":"absent"}`)); err == nil {
		t.Fatal("unsupported Provider silently fell back")
	}
}
