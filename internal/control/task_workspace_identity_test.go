package control

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/tasks"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/executor"
	"github.com/jjyr/pot/pkg/protocol"
)

func managedWorkspaceFixture(t *testing.T) (*Service, tasks.Launch, *tasks.Workspace) {
	t.Helper()
	s := taskResourceService(t)
	s.options.WorkspaceHome = t.TempDir()
	source := t.TempDir()
	fixtureGit(t, source, "init", "--initial-branch=trunk")
	fixtureGit(t, source, "commit", "--allow-empty", "-m", "base")
	l := workspaceLaunch(t, s, "identity", protocol.TaskWorkspace{Kind: "worktree", ProjectDir: source, Commit: fixtureGit(t, source, "rev-parse", "HEAD")})
	w, err := s.prepareTaskWorkspace(context.Background(), l, MachinePolicy{ExecutionAccess: "allow"})
	if err != nil {
		t.Fatal(err)
	}
	return s, l, w
}

func nextWorkspaceRun(t *testing.T, s *Service, l tasks.Launch) tasks.Launch {
	t.Helper()
	ctx := context.Background()
	if err := s.RunJournal.RecordCleanup(ctx, l.ControlID, l.TaskID, l.RunID); err != nil {
		t.Fatal(err)
	}
	if err := s.RunJournal.Finish(ctx, l.ControlID, l.TaskID, l.RunID, tasks.Terminal{Outcome: core.Success}); err != nil {
		t.Fatal(err)
	}
	l.Generation++
	l.RunID = fmt.Sprintf("identity-run-%d", l.Generation)
	if _, err := s.RunJournal.Accept(ctx, l); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunJournal.Claim(ctx, l.ControlID, l.TaskID, l.RunID); err != nil {
		t.Fatal(err)
	}
	return l
}

func TestManagedWorkspaceContinuationKeepsCreationRecords(t *testing.T) {
	t.Parallel()
	s, l, original := managedWorkspaceFixture(t)
	ctx := context.Background()
	writeTaskFixture(t, filepath.Join(original.Path, "result.txt"), "original files")
	changes := []struct {
		name string
		args []string
	}{
		{"unchanged", nil},
		{"rename", []string{"branch", "-m", "renamed-feature"}},
		{"rename-again", []string{"branch", "-m", "renamed-again"}},
		{"checkout", []string{"checkout", "-b", "other-feature"}},
		{"advance", []string{"commit", "--allow-empty", "-m", "advance"}},
		{"rebase", nil},
		{"detached", []string{"checkout", "--detach", "HEAD"}},
	}
	for _, change := range changes {
		t.Run(change.name, func(t *testing.T) {
			if change.name == "rebase" {
				fixtureGit(t, original.Source, "commit", "--allow-empty", "-m", "new base")
				base := fixtureGit(t, original.Source, "rev-parse", "HEAD")
				fixtureGit(t, original.Path, "rebase", "--onto", base, original.Commit)
				if fixtureGit(t, original.Path, "rev-parse", "HEAD^") != base {
					t.Fatal("fixture did not rebase onto new base")
				}
			}
			if change.args != nil {
				fixtureGit(t, original.Path, change.args...)
			}
			l = nextWorkspaceRun(t, s, l)
			resumed, err := s.prepareTaskWorkspace(ctx, l, MachinePolicy{ExecutionAccess: "allow"})
			if err != nil || !reflect.DeepEqual(resumed, original) {
				t.Fatalf("continuation changed creation record: %+v %v", resumed, err)
			}
			found, err := s.RunJournal.TaskWorkspace(ctx, l.TaskID)
			if err != nil || !reflect.DeepEqual(found, original) {
				t.Fatalf("cross-run lookup: %+v %v", found, err)
			}
			file, err := (taskFiles{s}).ReadFile(ctx, l.ExecutionTarget, l.TaskID, "result.txt", 1024)
			if err != nil || string(file) != "original files" {
				t.Fatalf("file delivery: %q %v", file, err)
			}
			if _, err := s.host(l.TaskID, l.Generation); err != nil {
				t.Fatal("continued tool host", err)
			}
		})
	}
	if _, err := os.Stat(filepath.Join(original.Path, "result.txt")); err != nil {
		t.Fatal(err)
	}
}

func TestManagedWorkspaceContinuationRejectsChangedIdentity(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"missing", "replacement", "symlink", "other-repository", "other-worktree", "missing-git-file", "missing-admin", "redirected-admin", "changed-common", "redirected-common", "missing-backlink", "changed-backlink", "changed-toplevel", "protected-metadata"} {
		t.Run(scenario, func(t *testing.T) {
			s, l, original := managedWorkspaceFixture(t)
			ctx := context.Background()
			other := t.TempDir()
			fixtureGit(t, other, "init", "--initial-branch=other")
			fixtureGit(t, other, "commit", "--allow-empty", "-m", "other")
			switch scenario {
			case "missing", "replacement", "symlink":
				// Keep the original inode allocated, as required by the existing
				// device/inode directory identity policy on this platform.
				if err := os.Rename(original.Path, original.Path+"-retained"); err != nil {
					t.Fatal(err)
				}
				if scenario == "replacement" {
					if err := os.Mkdir(original.Path, 0700); err != nil {
						t.Fatal(err)
					}
				}
				if scenario == "symlink" {
					if err := os.Symlink(other, original.Path); err != nil {
						t.Fatal(err)
					}
				}
			case "other-repository":
				writeTaskFixture(t, filepath.Join(original.Path, ".git"), "gitdir: "+filepath.Join(other, ".git")+"\n")
			case "other-worktree", "redirected-admin":
				second := filepath.Join(t.TempDir(), "second")
				fixtureGit(t, original.Source, "worktree", "add", "-b", "second", second)
				admin := fixtureGit(t, second, "rev-parse", "--absolute-git-dir")
				if scenario == "other-worktree" {
					writeTaskFixture(t, filepath.Join(original.Path, ".git"), "gitdir: "+admin+"\n")
				} else {
					if err := os.Rename(original.GitDirs[0], original.GitDirs[0]+"-retained"); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(admin, original.GitDirs[0]); err != nil {
						t.Fatal(err)
					}
				}
			case "missing-git-file":
				if err := os.Remove(filepath.Join(original.Path, ".git")); err != nil {
					t.Fatal(err)
				}
			case "missing-admin":
				if err := os.Rename(original.GitDirs[0], original.GitDirs[0]+"-retained"); err != nil {
					t.Fatal(err)
				}
			case "changed-common":
				writeTaskFixture(t, filepath.Join(original.GitDirs[0], "commondir"), filepath.Join(other, ".git")+"\n")
			case "redirected-common":
				if err := os.Rename(original.GitDirs[1], original.GitDirs[1]+"-retained"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(other, ".git"), original.GitDirs[1]); err != nil {
					t.Fatal(err)
				}
			case "missing-backlink":
				if err := os.Remove(filepath.Join(original.GitDirs[0], "gitdir")); err != nil {
					t.Fatal(err)
				}
			case "changed-backlink":
				writeTaskFixture(t, filepath.Join(original.GitDirs[0], "gitdir"), filepath.Join(other, ".git")+"\n")
			case "changed-toplevel":
				fixtureGit(t, original.Path, "config", "extensions.worktreeConfig", "true")
				fixtureGit(t, original.Path, "config", "--worktree", "core.worktree", other)
			case "protected-metadata":
				protected := filepath.Join(s.options.Root, "private-repository")
				if err := os.Mkdir(protected, 0700); err != nil {
					t.Fatal(err)
				}
				fixtureGit(t, protected, "init")
				writeTaskFixture(t, filepath.Join(original.Path, ".git"), "gitdir: "+filepath.Join(protected, ".git")+"\n")
			}
			l = nextWorkspaceRun(t, s, l)
			if _, err := s.prepareTaskWorkspace(ctx, l, MachinePolicy{ExecutionAccess: "ask"}); err == nil {
				t.Fatal("changed workspace accepted for continuation")
			}
			found, err := s.RunJournal.TaskWorkspace(ctx, l.TaskID)
			if err != nil || !reflect.DeepEqual(found, original) {
				t.Fatalf("failed continuation rebound permissions or creation records: %+v %v", found, err)
			}
			if scenario == "missing" {
				if _, err := os.Lstat(original.Path); !os.IsNotExist(err) {
					t.Fatal("missing workspace recreated", err)
				}
			}
			if scenario == "missing" || scenario == "replacement" || scenario == "symlink" {
				if root, err := s.openTaskWorkspace(ctx, l.TaskID); err == nil {
					root.Close()
					t.Fatal("changed directory accepted for delivery")
				}
			}
		})
	}
}

func TestRealManagedWorkspaceContinuationPermissions(t *testing.T) {
	t.Parallel()
	if os.Getenv("POT_SANDBOX_INTEGRATION") != "1" {
		t.Skip("requires real sandbox qualification")
	}
	s, l, original := managedWorkspaceFixture(t)
	var err error
	s.options.Node, err = exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	s.options.SandboxDirectory = os.Getenv("POT_SANDBOX_SDK")
	fixtureGit(t, original.Path, "branch", "-m", "renamed-feature")
	fixtureGit(t, original.Path, "checkout", "--detach", "HEAD")
	l = nextWorkspaceRun(t, s, l)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if _, err := s.prepareTaskWorkspace(ctx, l, MachinePolicy{ExecutionAccess: "ask"}); err != nil {
		t.Fatal(err)
	}
	engine, err := s.host(l.TaskID, l.Generation)
	if err != nil {
		t.Fatal(err)
	}
	writeTaskFixture(t, filepath.Join(original.Path, "result.txt"), "proof")
	writeTaskFixture(t, filepath.Join(s.options.Root, "private.txt"), "private")
	commands := []string{
		"git add result.txt && git ls-files --error-unmatch result.txt",
		fmt.Sprintf("if cat %s/private.txt; then exit 21; fi; if printf bad > %s/private.txt; then exit 22; fi", quoteFixturePath(s.options.Root), quoteFixturePath(s.options.Root)),
	}
	for i, command := range commands {
		request := executor.Request{ID: fmt.Sprintf("continued-%d", i), Mode: "sandbox", Revision: 1, CWD: original.Path, Policy: l.Config.Params.Agent.Shell, Operation: "exec_command", Args: map[string]any{"cmd": command, "login": false, "yield_time_ms": 1}}
		result, err := engine.Execute(ctx, request, nil)
		for poll := 0; err == nil && result["session_id"] != nil; poll++ {
			request.ID = fmt.Sprintf("continued-%d-poll-%d", i, poll)
			request.Mode = "session"
			request.Operation = "write_stdin"
			request.Args = map[string]any{"session_id": result["session_id"], "yield_time_ms": 1000}
			result, err = engine.Execute(ctx, request, nil)
		}
		if err != nil || result["exit_code"] != 0 {
			t.Fatalf("continued sandbox command %d: %v %v", i, result, err)
		}
	}
	file, err := (taskFiles{s}).ReadFile(ctx, l.ExecutionTarget, l.TaskID, "result.txt", 1024)
	if err != nil || string(file) != "proof" {
		t.Fatalf("continued delivery: %q %v", file, err)
	}
}
