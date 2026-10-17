package runtime

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/control"
	"github.com/jjyr/pot/internal/secrets"
)

func TestPluginSandboxStartWaitsForConsentBeforeAnyEngineStart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	keys, _ := secrets.NewMemoryKey(make([]byte, 32))
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	r, err := Open(ctx, Options{Root: root, ResourceDirectory: providerResources(t), DisableMetadataRefresh: true, Control: control.Options{Keys: keys}})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	d, err := r.Sandboxes.EnsureShared(ctx)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := r.Control.ManageMachine(ctx, control.MachineCommand{Action: "start", MachineID: r.Root.Identity.RuntimeID, SandboxID: d.ID})
		done <- err
	}()
	for {
		status, err := r.Provisioning.Snapshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(status.Requests) > 0 {
			request := status.Requests[0]
			if request.State != "awaiting_consent" {
				t.Fatal(request)
			}
			select {
			case err := <-done:
				t.Fatal("start finished without resource consent", err)
			default:
			}
			if _, err = r.Provisioning.Cancel(request.ID); err != nil {
				t.Fatal(err)
			}
			break
		}
		select {
		case err := <-done:
			t.Fatal("start bypassed resource consent", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled consent started Helper")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// There are deliberately no Helper binaries: reaching lifecycle start would
	// fail before this durable awaiting-consent flow could be observed.
}

func providerResources(t *testing.T) string {
	t.Helper()
	resources := t.TempDir()
	directory := filepath.Join(resources, "providers", "podman")
	if err := os.MkdirAll(filepath.Join(directory, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"provider.json", "catalog.json"} {
		data, err := os.ReadFile(filepath.Join("../../providers/podman", name))
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(directory, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	build := exec.Command("go", "build", "-o", filepath.Join(directory, "bin/provider"), "../../providers/podman/cmd")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("provider build: %s %v", output, err)
	}
	return resources
}
