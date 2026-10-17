package controlplane

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestMachineLabelsDoNotRequireOrRestoreImplicitTaskRunner(t *testing.T) {
	for _, mode := range []string{"neutral", "embedded", "control"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "directory.db")
			options := Options{RuntimeID: "local", Embedded: mode == "embedded"}
			d, err := Open(path, options)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "control" {
				if err = d.EnableControl(); err != nil {
					t.Fatal(err)
				}
			}
			identity := d.Identity()
			if slices.Contains(d.LocalLabels(), "task-runner") {
				t.Fatal("new root advertised obsolete implicit routing label")
			}
			if err = d.SetLabels(context.Background(), "local", nil); err != nil {
				t.Fatal(err)
			}
			if err = d.Close(); err != nil {
				t.Fatal(err)
			}
			d, err = Open(path, options)
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			if d.Identity() != identity || len(d.LocalLabels()) != 0 {
				t.Fatal("restart restored implicit labels or changed identity", d.LocalLabels())
			}
		})
	}
}

func TestRemoteMemberRemovalDoesNotRequireTaskRunnerReplacement(t *testing.T) {
	ctx := context.Background()
	control, _, peer := pair(t, nil, nil)
	if err := control.SetLabels(ctx, "control", nil); err != nil {
		t.Fatal(err)
	}
	if err := control.SetLabels(ctx, "worker", []string{"task-runner"}); err != nil {
		t.Fatal(err)
	}
	if err := control.Remove(ctx, "worker"); err != nil {
		t.Fatal("obsolete label prevented membership revocation", err)
	}
	select {
	case <-peer.Done():
	case <-time.After(time.Second):
		t.Fatal("removed member connection remained active")
	}
	members, err := control.Machines(ctx)
	if err != nil || len(members) != 0 {
		t.Fatal("revoked member remained enabled", members, err)
	}
	if err = control.SetLabels(ctx, "control", []string{"custom"}); err != nil {
		t.Fatal("label edit required a task-runner", err)
	}
}
