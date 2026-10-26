package control

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/tasks"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/execution"
	"github.com/jjyr/pot/pkg/executor"
	"github.com/jjyr/pot/pkg/protocol"
)

func TestTemporaryWorkspaceUsesReceivingWorkspace(t *testing.T) {
	t.Parallel()
	for _, variant := range []string{"default", "custom", "workspace-alias", "root-alias"} {
		t.Run(variant, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), ".pot")
			workspace := ""
			switch variant {
			case "custom":
				workspace = filepath.Join(t.TempDir(), "custom-workspace")
			case "workspace-alias":
				workspace = filepath.Join(t.TempDir(), "alias")
				if err := os.Symlink(t.TempDir(), workspace); err != nil {
					t.Fatal(err)
				}
			case "root-alias":
				if err := os.Symlink(t.TempDir(), root); err != nil {
					t.Fatal(err)
				}
			}
			s := taskResourceServiceAt(t, root, workspace)
			l := workspaceLaunch(t, s, "new-task", protocol.TaskWorkspace{Kind: "temporary"})
			w, err := s.prepareTaskWorkspace(context.Background(), l, MachinePolicy{ExecutionAccess: "allow"})
			if err != nil {
				t.Fatal(err)
			}
			effective := workspace
			if effective == "" {
				effective = filepath.Join(root, "agent-workspace")
			}
			canonical, err := filepath.EvalSymlinks(effective)
			if err != nil {
				t.Fatal(err)
			}
			if filepath.Dir(w.Path) != filepath.Join(canonical, "tasks") || w.DirectoryID == "" || w.Status != "ready" {
				t.Fatalf("workspace: %+v", w)
			}
			for _, dir := range []string{w.Path, filepath.Dir(w.Path)} {
				info, err := os.Stat(dir)
				if err != nil || info.Mode().Perm() != 0700 {
					t.Fatalf("private directory %s: %v %v", dir, info, err)
				}
			}
			writeTaskFixture(t, filepath.Join(w.Path, "proof.txt"), "persistent proof")
			data, err := (taskFiles{s}).ReadFile(context.Background(), l.ExecutionTarget, l.TaskID, "proof.txt", 1024)
			if err != nil || string(data) != "persistent proof" {
				t.Fatalf("delivery: %q %v", data, err)
			}
			// Both direct traversal and a workspace symlink must stay confined.
			writeTaskFixture(t, filepath.Join(s.options.Root, "authority.txt"), "private fixture")
			private := filepath.Join(s.options.Root, "authority-directory")
			if err := os.Mkdir(private, 0700); err != nil {
				t.Fatal(err)
			}
			if _, err := s.workspaceDirectory(private); err == nil {
				t.Fatal("Runtime authority directory accepted as a workspace")
			}
			if err := os.Symlink(filepath.Join(s.options.Root, "authority.txt"), filepath.Join(w.Path, "escape")); err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{"../../authority.txt", "escape"} {
				if _, err := (taskFiles{s}).ReadFile(context.Background(), l.ExecutionTarget, l.TaskID, path, 1024); err == nil {
					t.Fatalf("delivery escaped via %s", path)
				}
			}
		})
	}
}

func TestTemporaryWorkspaceAllocationFailureIsFenced(t *testing.T) {
	t.Parallel()
	for _, variant := range []string{"file", "symlink-temp", "symlink-authority", "symlink-within-workspace", "writable-by-others", "unwritable", "missing-root", "authority-root"} {
		t.Run(variant, func(t *testing.T) {
			s := taskResourceService(t)
			parent := filepath.Join(s.options.Workspace, "tasks")
			var target string
			switch variant {
			case "file":
				writeTaskFixture(t, parent, "occupied")
			case "symlink-temp":
				target = t.TempDir()
			case "symlink-authority":
				target = s.options.Root
			case "symlink-within-workspace":
				target = filepath.Join(s.options.Workspace, "redirect")
				if err := os.Mkdir(target, 0700); err != nil {
					t.Fatal(err)
				}
			case "writable-by-others", "unwritable":
				if err := os.Mkdir(parent, 0700); err != nil {
					t.Fatal(err)
				}
				mode := os.FileMode(0777)
				if variant == "unwritable" {
					mode = 0500
				}
				if err := os.Chmod(parent, mode); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { os.Chmod(parent, 0700) })
				if variant == "unwritable" && os.Geteuid() == 0 {
					t.Skip("root bypasses filesystem write permissions")
				}
			case "missing-root":
				if err := os.Remove(s.options.Workspace); err != nil {
					t.Fatal(err)
				}
			case "authority-root":
				s.options.Workspace = s.options.Root
			}
			if target != "" {
				if err := os.Symlink(target, parent); err != nil {
					t.Fatal(err)
				}
			}
			ctx := context.Background()
			l := workspaceLaunch(t, s, "failed", protocol.TaskWorkspace{})
			if w, err := s.prepareTaskWorkspace(ctx, l, MachinePolicy{ExecutionAccess: "allow"}); err == nil || w != nil {
				t.Fatalf("unexpected fallback: %+v %v", w, err)
			}
			saved, claim, err := s.RunJournal.BeginWorkspace(ctx, l.ControlID, l.TaskID, l.RunID)
			if err != nil || claim || saved == nil || saved.Status != "failed" || saved.Path != "" {
				t.Fatalf("failure fence: %+v %v", saved, err)
			}
			if target != "" {
				entries, err := os.ReadDir(target)
				if err != nil {
					t.Fatal(err)
				}
				for _, entry := range entries {
					if len(entry.Name()) > 9 && entry.Name()[:9] == "pot-task-" {
						t.Fatal("created through redirected parent")
					}
				}
			}
			if w, err := s.prepareTaskWorkspace(ctx, l, MachinePolicy{ExecutionAccess: "allow"}); err == nil || w != nil {
				t.Fatal("failed allocation replayed", w, err)
			}
		})
	}
}

func TestTemporaryWorkspaceConcurrentAllocation(t *testing.T) {
	t.Parallel()
	s := taskResourceService(t)
	const count = 16
	launches := make([]tasks.Launch, count)
	for i := range launches {
		launches[i] = workspaceLaunch(t, s, fmt.Sprintf("concurrent-%d", i), protocol.TaskWorkspace{})
	}
	type result struct {
		w   *tasks.Workspace
		err error
	}
	results := make(chan result, count)
	var wg sync.WaitGroup
	for _, l := range launches {
		wg.Go(func() {
			w, err := s.prepareTaskWorkspace(context.Background(), l, MachinePolicy{ExecutionAccess: "allow"})
			results <- result{w, err}
		})
	}
	wg.Wait()
	close(results)
	seen := map[string]bool{}
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		if seen[result.w.Path] || filepath.Dir(result.w.Path) != filepath.Join(s.options.Workspace, "tasks") {
			t.Fatal("allocation reused or escaped", result.w)
		}
		seen[result.w.Path] = true
	}
}

func TestTemporaryWorkspaceRestartPreservesOldAndNewBindings(t *testing.T) {
	t.Parallel()
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprintf("legacy-%v", legacy), func(t *testing.T) {
			s := taskResourceService(t)
			ctx := context.Background()
			l := workspaceLaunch(t, s, "retained", protocol.TaskWorkspace{})
			var original *tasks.Workspace
			var err error
			if legacy {
				// A pre-change ready receipt in OS temporary storage. The fixture uses
				// the test runner's private TMPDIR rather than touching historical Tasks.
				dir := t.TempDir()
				dir, err = filepath.EvalSymlinks(dir)
				if err != nil {
					t.Fatal(err)
				}
				info, err := os.Stat(dir)
				if err != nil {
					t.Fatal(err)
				}
				id, err := taskDirectoryIdentity(info)
				if err != nil {
					t.Fatal(err)
				}
				original = &tasks.Workspace{Kind: "temporary", Path: dir, DirectoryID: id, Status: "ready"}
				if _, _, err := s.RunJournal.BeginWorkspace(ctx, l.ControlID, l.TaskID, l.RunID); err != nil {
					t.Fatal(err)
				}
				if err := s.RunJournal.SaveWorkspace(ctx, l.ControlID, l.TaskID, l.RunID, *original); err != nil {
					t.Fatal(err)
				}
			} else {
				original, err = s.prepareTaskWorkspace(ctx, l, MachinePolicy{ExecutionAccess: "allow"})
				if err != nil {
					t.Fatal(err)
				}
			}
			writeTaskFixture(t, filepath.Join(original.Path, "retained.txt"), "original files")
			if err := s.RunJournal.RecordCleanup(ctx, l.ControlID, l.TaskID, l.RunID); err != nil {
				t.Fatal(err)
			}
			if err := s.RunJournal.Finish(ctx, l.ControlID, l.TaskID, l.RunID, tasks.Terminal{Outcome: core.Cancelled}); err != nil {
				t.Fatal(err)
			}
			options := s.options
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			options.BootID = "restarted"
			options.Manager, err = execution.New("local", options.BootID, execution.Limits{MaxActive: 1, MaxQueued: 8})
			if err != nil {
				t.Fatal(err)
			}
			restarted, err := Open(options)
			if err != nil {
				t.Fatal(err)
			}
			defer restarted.Close()
			l.RunID = "continued"
			l.Generation++
			if _, err := restarted.RunJournal.Accept(ctx, l); err != nil {
				t.Fatal(err)
			}
			if _, err := restarted.RunJournal.Claim(ctx, l.ControlID, l.TaskID, l.RunID); err != nil {
				t.Fatal(err)
			}
			resumed, err := restarted.prepareTaskWorkspace(ctx, l, MachinePolicy{ExecutionAccess: "allow"})
			if err != nil || !reflect.DeepEqual(resumed, original) {
				t.Fatalf("binding changed after cancellation/restart: %+v %v", resumed, err)
			}
			data, err := (taskFiles{restarted}).ReadFile(ctx, l.ExecutionTarget, l.TaskID, "retained.txt", 1024)
			if err != nil || string(data) != "original files" {
				t.Fatalf("delivery after restart: %q %v", data, err)
			}
			if legacy {
				if _, err := os.Lstat(filepath.Join(options.Workspace, "tasks")); !os.IsNotExist(err) {
					t.Fatal("legacy continuation allocated a new workspace", err)
				}
			}
		})
	}
}

func TestRealTemporaryWorkspacePermissions(t *testing.T) {
	t.Parallel()
	if os.Getenv("POT_SANDBOX_INTEGRATION") != "1" {
		t.Skip("requires real sandbox qualification")
	}
	s := taskResourceService(t)
	var err error
	s.options.Node, err = exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	s.options.SandboxDirectory = os.Getenv("POT_SANDBOX_SDK")
	l := workspaceLaunch(t, s, "sandbox-task", protocol.TaskWorkspace{})
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	w, err := s.prepareTaskWorkspace(ctx, l, MachinePolicy{ExecutionAccess: "ask"})
	if err != nil {
		t.Fatal(err)
	}
	sibling := filepath.Join(s.options.Workspace, "tasks", "other-task", "private.txt")
	authority := filepath.Join(s.options.Root, "authentication-fixture.txt")
	assistant := filepath.Join(s.options.Workspace, "assistants", "other", "private.txt")
	for _, file := range []string{sibling, authority, assistant} {
		writeTaskFixture(t, file, "private fixture")
	}
	engine, err := s.host(l.TaskID, l.Generation)
	if err != nil {
		t.Fatal(err)
	}
	command := "printf proof > proof.txt && test -r proof.txt"
	for _, file := range []string{sibling, authority, assistant} {
		path := quoteFixturePath(file)
		command += "; if cat " + path + "; then exit 21; fi"
		if runtime.GOOS == "linux" {
			// SRT may allow writes only in its private hidden-directory
			// overlay. Verify below that none reaches the host files.
			command += "; printf bad > " + path + " || :"
		} else {
			command += "; if printf bad > " + path + "; then exit 22; fi"
		}
	}
	request := executor.Request{ID: "persistent-sandbox", Mode: "sandbox", Revision: 1, CWD: w.Path, Policy: l.Config.Params.Agent.Shell, Operation: "exec_command", Args: map[string]any{"cmd": command, "login": false, "yield_time_ms": 1000}}
	result, err := engine.Execute(ctx, request, nil)
	for poll := 0; err == nil && result["session_id"] != nil; poll++ {
		request.ID = fmt.Sprintf("persistent-poll-%d", poll)
		request.Mode = "session"
		request.Operation = "write_stdin"
		request.Args = map[string]any{"session_id": result["session_id"], "yield_time_ms": 1000}
		result, err = engine.Execute(ctx, request, nil)
	}
	if err != nil || result["exit_code"] != 0 {
		t.Fatalf("protected workspace: %v %v", result, err)
	}
	data, err := (taskFiles{s}).ReadFile(ctx, l.ExecutionTarget, l.TaskID, "proof.txt", 1024)
	if err != nil || string(data) != "proof" {
		t.Fatalf("protected delivery: %q %v", data, err)
	}
	for _, file := range []string{sibling, authority, assistant} {
		data, err := os.ReadFile(file)
		if err != nil || string(data) != "private fixture" {
			t.Fatalf("protected file changed: %q %v", data, err)
		}
	}
}
