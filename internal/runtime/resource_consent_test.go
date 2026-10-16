package runtime

import (
	"context"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/control"
	"github.com/jjyr/pot/internal/secrets"
)

func TestManualAppleSandboxStartWaitsForConsentBeforeAnyHelperStart(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Apple container resource consent")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	keys, _ := secrets.NewMemoryKey(make([]byte, 32))
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	r, err := Open(ctx, Options{Root: root, ResourceDirectory: t.TempDir(), HelperDirectory: t.TempDir(), DisableMetadataRefresh: true, Control: control.Options{Keys: keys}})
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
