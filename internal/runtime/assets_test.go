package runtime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jjyr/pot/internal/control"
	"github.com/jjyr/pot/internal/secrets"
)

func TestNativeRuntimeLeavesLegacyDisksUntouchedAndRejectsRemovedResources(t *testing.T) {
	ctx := context.Background()
	keys, err := secrets.NewMemoryKey(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	options := Options{Root: filepath.Join(t.TempDir(), "runtime"), ResourceDirectory: t.TempDir(), DisableMetadataRefresh: true, Control: control.Options{Keys: keys}}
	first, err := Open(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	if err = first.Close(); err != nil {
		t.Fatal(err)
	}
	disk := filepath.Join(options.Root, "execution", "retained", "disk.raw")
	if err = os.MkdirAll(filepath.Dir(disk), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(disk, []byte("owner-managed disk"), 0600); err != nil {
		t.Fatal(err)
	}
	host, err := Open(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	body, err := os.ReadFile(disk)
	if err != nil || string(body) != "owner-managed disk" {
		t.Fatal("legacy disk changed", err)
	}
	for _, name := range []string{"resource-cache", "execution/sandboxes.sqlite"} {
		if _, err = os.Stat(filepath.Join(options.Root, name)); !os.IsNotExist(err) {
			t.Fatal("native startup prepared retired resources", name, err)
		}
	}
	snapshot, err := host.ResourceSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var view map[string]json.RawMessage
	if err = json.Unmarshal(raw, &view); err != nil {
		t.Fatal(err)
	}
	if len(view) != 3 || view["runtime_id"] == nil || view["assistants"] == nil || view["commands"] == nil {
		t.Fatal("resource snapshot includes retired environment state", string(raw))
	}
	for _, request := range []string{
		`{"action":"create","kind":"host","request_id":"old-host"}`,
		`{"action":"start","kind":"sandbox","sandbox_id":"shared","request_id":"old-start"}`,
	} {
		if _, err = host.ResourceCall(ctx, "POST", json.RawMessage(request)); err == nil {
			t.Fatal("removed lifecycle command accepted", request)
		}
	}
}
