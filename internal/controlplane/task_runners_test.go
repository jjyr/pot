package controlplane

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"sync"
	"testing"
)

func TestControlDefaultsToTaskRunner(t *testing.T) {
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
			if !slices.Contains(d.LocalLabels(), "task-runner") {
				t.Fatal("local Control has no default task-runner label")
			}
			if err = d.SetLabels(context.Background(), "local", nil); err == nil {
				t.Fatal("removed the Space's only task-runner label")
			}
			if err = d.Close(); err != nil {
				t.Fatal(err)
			}
			d, err = Open(path, options)
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			if d.Identity() != identity || !slices.Contains(d.LocalLabels(), "task-runner") {
				t.Fatal("rejected label removal changed persisted identity or routing")
			}
		})
	}
}

func TestLastTaskRunnerGuardsLabelsAndRevocation(t *testing.T) {
	ctx := context.Background()
	control, _, peer := pair(t, nil, nil)
	if err := control.SetLabels(ctx, "worker", []string{"linux", "task-runner"}); err != nil {
		t.Fatal(err)
	}
	if err := control.SetLabels(ctx, "control", []string{"custom"}); err != nil {
		t.Fatal(err)
	}
	control.mu.Lock()
	before := control.state.Members["worker"]
	control.mu.Unlock()
	if err := control.SetLabels(ctx, "worker", []string{"linux"}); err == nil {
		t.Fatal("removed the last task-runner label")
	}
	if err := control.Remove(ctx, "worker"); err == nil {
		t.Fatal("revoked the last task-runner member")
	}
	control.mu.Lock()
	after := control.state.Members["worker"]
	control.mu.Unlock()
	if !after.Machine.Enabled || after.Token != before.Token || !slices.Equal(after.Machine.Labels, before.Machine.Labels) {
		t.Fatal("rejected removal changed member labels or authority")
	}
	select {
	case <-peer.Done():
		t.Fatal("rejected removal closed the member connection")
	default:
	}
	// Repeated activation must not restore a deliberately removed local label.
	if err := control.EnableControl(); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(control.LocalLabels(), "task-runner") {
		t.Fatal("repeated Control activation restored removed label")
	}
	peer.Close()
	// Offline members still satisfy the configuration invariant.
	if err := control.SetLabels(ctx, "control", []string{}); err != nil {
		t.Fatal(err)
	}
	if err := control.SetLabels(ctx, "control", []string{"task-runner"}); err != nil {
		t.Fatal(err)
	}
	if err := control.Remove(ctx, "worker"); err != nil {
		t.Fatal(err)
	}
	if err := control.SetLabels(ctx, "control", nil); err == nil {
		t.Fatal("revoked member still counted as a task-runner")
	}
}

func TestOpenSuppliesMissingDefaultAndPreservesRemoteTaskRunner(t *testing.T) {
	ctx := context.Background()
	control, _, _ := pair(t, nil, nil)
	for _, remoteRunner := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing-default", true: "remote-default"}[remoteRunner], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "directory.db")
			options := Options{RuntimeID: "control"}
			d, err := Open(path, options)
			if err != nil {
				t.Fatal(err)
			}
			// Seed an existing Space to verify startup initialization independently
			// of the guards on owner edits.
			control.mu.Lock()
			raw, err := json.Marshal(control.state)
			control.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			var saved book
			if err = json.Unmarshal(raw, &saved); err != nil {
				t.Fatal(err)
			}
			saved.LocalLabels = []string{"custom"}
			m := saved.Members["worker"]
			if remoteRunner {
				m.Machine.Labels = []string{"task-runner"}
			}
			saved.Members["worker"] = m
			raw, err = json.Marshal(saved)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = d.db.ExecContext(ctx, "UPDATE controlplane_state SET body=? WHERE id=1", raw); err != nil {
				t.Fatal(err)
			}
			d.Close()
			for range 2 {
				d, err = Open(path, options)
				if err != nil {
					t.Fatal(err)
				}
				local := d.LocalLabels()
				if slices.Contains(local, "task-runner") == remoteRunner || !slices.Contains(local, "custom") {
					t.Fatal("startup lost custom labels or changed the configured destination", local)
				}
				d.Close()
			}
		})
	}
}

func TestConcurrentTaskRunnerRemovalsRetainOneDestination(t *testing.T) {
	ctx := context.Background()
	control, _, _ := pair(t, nil, nil)
	if err := control.SetLabels(ctx, "worker", []string{"task-runner"}); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, remove := range []func() error{
		func() error { return control.SetLabels(ctx, "control", nil) },
		func() error { return control.Remove(ctx, "worker") },
	} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- remove()
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	succeeded := 0
	for err := range results {
		if err == nil {
			succeeded++
		}
	}
	if succeeded != 1 {
		t.Fatalf("expected exactly one removal, got %d", succeeded)
	}
	machines, err := control.Machines(ctx)
	if err != nil {
		t.Fatal(err)
	}
	hasRunner := slices.Contains(control.LocalLabels(), "task-runner")
	for _, m := range machines {
		hasRunner = hasRunner || slices.Contains(m.Labels, "task-runner")
	}
	if !hasRunner {
		t.Fatal("concurrent updates removed every task-runner")
	}
}
