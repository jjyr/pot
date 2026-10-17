package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/control"
	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/pkg/resource"
	"github.com/jjyr/pot/pkg/sandbox"
	"github.com/jjyr/pot/pkg/sandboxresources"
)

type resourceProvider struct {
	boundID  string
	prepared sandbox.PreparedResources
}

func (p *resourceProvider) Descriptor() sandbox.Descriptor {
	return sandbox.Descriptor{ID: "alternate", OS: "custom", HostOS: runtime.GOOS}
}
func (p *resourceProvider) DefaultResources() sandbox.Resources {
	return sandbox.Resources{CPUs: 1, MemoryGiB: 1}
}
func (p *resourceProvider) ValidateResources(sandbox.Instance, sandbox.Resources) error { return nil }
func (p *resourceProvider) Workspace(i sandbox.Instance) string {
	return filepath.Join(i.Directory(), "workspace")
}
func (p *resourceProvider) GuestWorkspace() string               { return "/workspace" }
func (p *resourceProvider) CleanupPolicy() sandbox.CleanupPolicy { return sandbox.ProcessContainment }
func (p *resourceProvider) BindResources(_ context.Context, i sandbox.Instance, r sandbox.PreparedResources) error {
	p.boundID = i.ID
	p.prepared = r
	return nil
}
func (p *resourceProvider) Lifecycle(_ context.Context, _ sandbox.Instance, method string) (sandbox.Status, error) {
	if method != "status" {
		return sandbox.Status{}, errors.New("offline import must not start guest lifecycle")
	}
	return sandbox.Status{Phase: "stopped"}, nil
}
func (p *resourceProvider) Health(context.Context, sandbox.Instance) (bool, error) {
	return false, errors.New("offline import must not call guest")
}
func (p *resourceProvider) Request(context.Context, sandbox.Instance, string, json.RawMessage, time.Duration) (json.RawMessage, error) {
	return nil, errors.New("offline import must not call guest")
}
func (p *resourceProvider) Shutdown(context.Context) error { return nil }
func TestOfflineImportTargetsExplicitProviderAndOpaqueBundle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	data := []byte("alternate VM disk")
	hash := sha256.Sum256(data)
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "disk.raw"), data, 0600); err != nil {
		t.Fatal(err)
	}
	catalog := sandboxresources.Catalog{SchemaVersion: 3, Defaults: map[string]map[string]string{"alternate": {runtime.GOARCH: "alternate-disk"}}, Resources: map[string]resource.Resource{"alternate-asset": {ReleaseTag: "fixture-v1", Filename: "disk.raw", SHA256: hex.EncodeToString(hash[:]), SizeBytes: uint64(len(data)), UnpackedBytes: uint64(len(data)), Format: resource.Raw}}, Bundles: map[string]sandboxresources.Bundle{"alternate-disk": {Provider: "alternate", Architecture: runtime.GOARCH, Repository: "https://example.test", Resources: map[string]string{"disk": "alternate-asset"}, Metadata: json.RawMessage(`{"boot_format":"custom-v9"}`)}}}
	catalog.Defaults = map[string]map[string]string{}
	keys, _ := secrets.NewMemoryKey(make([]byte, 32))
	provider := &resourceProvider{}
	host, err := Open(ctx, Options{Root: filepath.Join(t.TempDir(), "runtime"), SandboxProviders: []sandbox.Provider{provider}, SandboxCatalog: &catalog, DisableMetadataRefresh: true, Control: control.Options{Keys: keys}})
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	if _, err = host.Sandboxes.Create(ctx, "custom", "Custom image", "alternate", sandbox.Resources{}); err != nil {
		t.Fatal(err)
	}
	_, startErr := host.Control.ManageMachine(ctx, control.MachineCommand{Action: "start", MachineID: host.Root.Identity.RuntimeID, SandboxID: "custom"})
	if startErr == nil || !strings.Contains(startErr.Error(), "unknown resource bundle") {
		t.Fatal("missing default bypassed resource preparation", startErr)
	}
	if err = host.ImportSandboxResources(ctx, "custom", "alternate-disk", directory); err != nil {
		t.Fatal(err)
	}
	if provider.boundID != "custom" || provider.prepared.Provider != "alternate" || string(provider.prepared.Metadata) != `{"boot_format":"custom-v9"}` {
		t.Fatal(provider)
	}
	imported, err := os.ReadFile(filepath.Join(provider.prepared.Paths["disk"], "payload"))
	if err != nil || string(imported) != string(data) {
		t.Fatal(string(imported), err)
	}
	if err = host.ImportSandboxResources(ctx, "missing", "alternate-disk", directory); err == nil {
		t.Fatal("unknown Sandbox import accepted")
	}
}
