package control

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jjyr/pot/internal/tasks"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/executor"
	"github.com/jjyr/pot/pkg/protocol"
)

func bindTestWorkspace(t *testing.T, s *Service, task, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	l := tasks.Launch{ControlID: s.options.RuntimeID, RuntimeID: s.options.RuntimeID, AssistantID: "main", TaskID: task, RunID: "fixture-" + task, Generation: 1, ExecutionTarget: core.RuntimeTarget(s.options.RuntimeID), Config: taskConfig(t), Plugins: []string{}, Text: "fixture", Workspace: protocol.TaskWorkspace{Kind: "directory", ProjectDir: dir}}
	ctx := context.Background()
	if _, err := s.RunJournal.Accept(ctx, l); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunJournal.Claim(ctx, l.ControlID, task, l.RunID); err != nil {
		t.Fatal(err)
	}
	w, err := s.prepareTaskWorkspace(ctx, l, MachinePolicy{ExecutionAccess: "allow"})
	if err != nil {
		t.Fatal(err)
	}
	return w.Path
}
func workspaceLaunch(t *testing.T, s *Service, id string, intent protocol.TaskWorkspace) tasks.Launch {
	t.Helper()
	l := tasks.Launch{ControlID: "local", RuntimeID: "local", AssistantID: "main", TaskID: id, RunID: "run-" + id, Generation: 1, ExecutionTarget: core.RuntimeTarget("local"), Config: taskConfig(t), Plugins: []string{}, Text: "workspace fixture", Workspace: intent}
	if _, err := s.RunJournal.Accept(context.Background(), l); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunJournal.Claim(context.Background(), "local", id, l.RunID); err != nil {
		t.Fatal(err)
	}
	return l
}
func fixtureGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	// Hooks export Git directory bindings for their invoking checkout. Fixtures
	// must use their own repositories even when the suite runs inside a hook.
	for _, variable := range os.Environ() {
		if !strings.HasPrefix(variable, "GIT_") {
			cmd.Env = append(cmd.Env, variable)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_AUTHOR_NAME=Pot fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=Pot fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}
func TestTaskWorkspaceRefreshesBaseAndUnifiesProjectTools(t *testing.T) {
	testTaskWorkspaceProjectTools(t, false)
}
func TestRealTaskWorktreePreparation(t *testing.T) {
	if os.Getenv("POT_SANDBOX_INTEGRATION") != "1" {
		t.Skip("requires real sandbox qualification")
	}
	testTaskWorkspaceProjectTools(t, true)
}
func testTaskWorkspaceProjectTools(t *testing.T, protected bool) {
	s := taskResourceService(t)
	s.options.WorkspaceHome = t.TempDir()
	policy := "allow"
	if protected {
		policy = "ask"
		s.options.SandboxDirectory = os.Getenv("POT_SANDBOX_SDK")
		var err error
		s.options.Node, err = exec.LookPath("node")
		if err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	remote := filepath.Join(t.TempDir(), "remote.git")
	fixtureGit(t, t.TempDir(), "init", "--bare", "--initial-branch=trunk", remote)
	source := filepath.Join(t.TempDir(), "project")
	fixtureGit(t, t.TempDir(), "clone", remote, source)
	writeTaskFixture(t, filepath.Join(source, "AGENTS.md"), "WORKSPACE_RULE_NONCE")
	writeTaskFixture(t, filepath.Join(source, "base.txt"), "first\n")
	fixtureGit(t, source, "add", ".")
	fixtureGit(t, source, "commit", "-m", "first")
	fixtureGit(t, source, "push", "origin", "trunk")
	upstream := filepath.Join(t.TempDir(), "upstream")
	fixtureGit(t, t.TempDir(), "clone", remote, upstream)
	writeTaskFixture(t, filepath.Join(upstream, "base.txt"), "fresh\n")
	fixtureGit(t, upstream, "add", ".")
	fixtureGit(t, upstream, "commit", "-m", "fresh")
	fixtureGit(t, upstream, "push", "origin", "trunk")
	fresh := fixtureGit(t, upstream, "rev-parse", "HEAD")
	old := fixtureGit(t, source, "rev-parse", "HEAD")
	writeTaskFixture(t, filepath.Join(source, "base.txt"), "dirty source\n")
	hook := filepath.Join(source, ".git", "hooks", "post-checkout")
	writeTaskFixture(t, hook, "#!/bin/sh\nprintf hook > '"+filepath.Join(source, "hook-ran")+"'\n")
	os.Chmod(hook, 0700)
	l := workspaceLaunch(t, s, "feature", protocol.TaskWorkspace{Kind: "worktree", ProjectDir: source})
	w, err := s.prepareTaskWorkspace(ctx, l, MachinePolicy{ExecutionAccess: policy})
	if err != nil {
		t.Fatal(err)
	}
	canonicalHome, _ := filepath.EvalSymlinks(s.options.WorkspaceHome)
	if w.Commit != fresh || w.Base != "trunk" || filepath.Base(w.Path) != "project" || len(filepath.Base(filepath.Dir(w.Path))) != 4 || !strings.HasPrefix(w.Path, filepath.Join(canonicalHome, ".pot", "worktrees")) {
		t.Fatalf("workspace: %+v", w)
	}
	if fixtureGit(t, source, "rev-parse", "HEAD") != old {
		t.Fatal("source checkout advanced")
	}
	if raw, _ := os.ReadFile(filepath.Join(source, "base.txt")); string(raw) != "dirty source\n" {
		t.Fatal("source edits changed")
	}
	if _, err := os.Stat(filepath.Join(source, "hook-ran")); !os.IsNotExist(err) {
		t.Fatal("repository hook ran during preparation")
	}
	e, err := s.prepareExecution(ctx, l.TaskID, l.ExecutionTarget, true, "")
	if err != nil || e.Workspace != w.Path || !strings.Contains(e.Instructions, "WORKSPACE_RULE_NONCE") {
		t.Fatalf("project context: %+v %v", e, err)
	}
	if err = s.addExecutionEnvironment(&e, l.ExecutionTarget); err != nil {
		t.Fatal(err)
	}
	for _, section := range e.ContextSections {
		if section.Key == "environment" && section.Text != "cwd: "+w.Path {
			t.Fatal(section)
		}
	}
	report, err := applyWorkspacePatch(w.Path)
	if err != nil || report == "" {
		t.Fatal(report, err)
	}
	if protected {
		engine, err := s.host(l.TaskID, l.Generation)
		if err != nil {
			t.Fatal(err)
		}
		result, err := engine.Execute(ctx, executor.Request{ID: "git-status", Mode: "sandbox", Revision: 1, CWD: w.Path, Policy: l.Config.Params.Agent.Shell, Operation: "exec_command", Args: map[string]any{"cmd": "git status --short", "login": false}}, nil)
		if err != nil || result["exit_code"] != 0 {
			t.Fatalf("protected worktree Git metadata: %v %v", result, err)
		}
	}
	// Advancing the remote and resuming never re-fetches or replaces this worktree.
	writeTaskFixture(t, filepath.Join(upstream, "new.txt"), "later")
	fixtureGit(t, upstream, "add", ".")
	fixtureGit(t, upstream, "commit", "-m", "later")
	fixtureGit(t, upstream, "push", "origin", "trunk")
	same, err := s.prepareTaskWorkspace(ctx, l, MachinePolicy{ExecutionAccess: policy})
	if err != nil || same.Path != w.Path || same.Commit != fresh {
		t.Fatal(same, err)
	}
	file, err := (taskFiles{s}).ReadFile(ctx, l.ExecutionTarget, l.TaskID, "patched.txt", 1024)
	if err != nil || string(file) != "proof\n" {
		t.Fatalf("download binding: %q %v", file, err)
	}
	if _, err := os.Stat(filepath.Join(s.options.Workspace, "tasks", l.TaskID)); !os.IsNotExist(err) {
		t.Fatal("redundant Task workspace created")
	}
	// Only the Runtime-managed worktree created by this test is removed.
	t.Cleanup(func() { fixtureGit(t, source, "worktree", "remove", "--force", w.Path) })
}
func TestTemporaryWorkspaceRetainsOriginalAndFencesLostPreparation(t *testing.T) {
	s := taskResourceService(t)
	ctx := context.Background()
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	t.Setenv("TMP", tmp)
	t.Setenv("TEMP", tmp)
	l := workspaceLaunch(t, s, "temporary", protocol.TaskWorkspace{})
	w, err := s.prepareTaskWorkspace(ctx, l, MachinePolicy{ExecutionAccess: "allow"})
	if err != nil {
		t.Fatal(err)
	}
	canonical, _ := filepath.EvalSymlinks(tmp)
	if !strings.HasPrefix(w.Path, canonical+string(filepath.Separator)) {
		t.Fatal(w.Path)
	}
	w2, err := s.prepareTaskWorkspace(ctx, l, MachinePolicy{ExecutionAccess: "allow"})
	if err != nil || w2.Path != w.Path {
		t.Fatal(w2, err)
	}
	if err = s.RunJournal.RecordCleanup(ctx, l.ControlID, l.TaskID, l.RunID); err != nil {
		t.Fatal(err)
	}
	if err = s.RunJournal.Finish(ctx, l.ControlID, l.TaskID, l.RunID, tasks.Terminal{Outcome: core.Success}); err != nil {
		t.Fatal(err)
	}
	l.RunID = "resume-temporary"
	if _, err = s.RunJournal.Accept(ctx, l); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RunJournal.Claim(ctx, l.ControlID, l.TaskID, l.RunID); err != nil {
		t.Fatal(err)
	}
	resumed, err := s.prepareTaskWorkspace(ctx, l, MachinePolicy{ExecutionAccess: "allow"})
	if err != nil || resumed.Path != w.Path {
		t.Fatal(resumed, err)
	}
	if _, err = s.taskWorkspacePath(ctx, l.TaskID); err != nil {
		t.Fatal(err)
	}
	os.RemoveAll(w.Path)
	if _, err = s.prepareTaskWorkspace(ctx, l, MachinePolicy{ExecutionAccess: "allow"}); err == nil {
		t.Fatal("lost workspace silently recreated")
	}
	os.MkdirAll(w.Path, 0700)
	if _, err = s.taskWorkspacePath(ctx, l.TaskID); err == nil {
		t.Fatal("replaced directory accepted for execution")
	}
	if root, e := s.openTaskWorkspace(ctx, l.TaskID); e == nil {
		root.Close()
		t.Fatal("replaced directory accepted for downloads")
	}
	interrupted := workspaceLaunch(t, s, "interrupted", protocol.TaskWorkspace{})
	s.RunJournal.BeginWorkspace(ctx, interrupted.ControlID, interrupted.TaskID, interrupted.RunID)
	if _, err = s.prepareTaskWorkspace(ctx, interrupted, MachinePolicy{ExecutionAccess: "allow"}); err == nil {
		t.Fatal("uncertain preparation replayed")
	}
	blocked := workspaceLaunch(t, s, "blocked", protocol.TaskWorkspace{})
	if _, err = s.prepareTaskWorkspace(ctx, blocked, MachinePolicy{ExecutionAccess: "deny"}); err == nil {
		t.Fatal("blocked machine prepared workspace")
	}
}

func TestTaskWorkspaceIdentitySurvivesWritesRenameAndRestart(t *testing.T) {
	s := taskResourceService(t)
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "workspace")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	l := workspaceLaunch(t, s, "instance", protocol.TaskWorkspace{Kind: "directory", ProjectDir: dir})
	w, err := s.prepareTaskWorkspace(ctx, l, MachinePolicy{ExecutionAccess: "allow"})
	if err != nil {
		t.Fatal(err)
	}
	path := w.Path
	writeTaskFixture(t, filepath.Join(path, "result.txt"), "original contents")
	checkPreparation := true
	check := func(accepted bool) {
		t.Helper()
		if checkPreparation {
			_, err := s.prepareTaskWorkspace(ctx, l, MachinePolicy{ExecutionAccess: "allow"})
			if (err == nil) != accepted {
				t.Fatalf("continued preparation: accepted=%v error=%v", accepted, err)
			}
		}
		_, err := s.taskWorkspacePath(ctx, "instance")
		if (err == nil) != accepted {
			t.Fatalf("execution root: accepted=%v error=%v", accepted, err)
		}
		root, err := s.openTaskWorkspace(ctx, "instance")
		if root != nil {
			root.Close()
		}
		if (err == nil) != accepted {
			t.Fatalf("download root: accepted=%v error=%v", accepted, err)
		}
	}
	check(true)
	if err := os.Remove(filepath.Join(path, "result.txt")); err != nil {
		t.Fatal(err)
	}
	writeTaskFixture(t, filepath.Join(path, "other.txt"), "preserve me")
	check(true) // ctime/mtime changed; the directory instance did not.
	retained := path + "-retained"
	if err := os.Rename(path, retained); err != nil {
		t.Fatal(err)
	}
	check(false)
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	check(false)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(retained, path); err != nil {
		t.Fatal(err)
	}
	check(false)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(retained, path); err != nil {
		t.Fatal(err)
	}
	check(true)
	options := s.options
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	checkPreparation = false // Run recovery is independent of persisted directory proof.
	check(true)
	if contents, err := os.ReadFile(filepath.Join(path, "other.txt")); err != nil || string(contents) != "preserve me" {
		t.Fatal("workspace contents changed", string(contents), err)
	}
}

func TestUnverifiedLegacyWorkspaceRequiresExplicitNewTask(t *testing.T) {
	s := taskResourceService(t)
	ctx := context.Background()
	l := workspaceLaunch(t, s, "legacy", protocol.TaskWorkspace{})
	w, err := s.prepareTaskWorkspace(ctx, l, MachinePolicy{ExecutionAccess: "allow"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(w.Path) })
	writeTaskFixture(t, filepath.Join(w.Path, "result.txt"), "original work")
	// Match the migration's outcome without inventing new original-instance proof.
	if _, err := s.Tasks.DB().Exec(`UPDATE run_workspaces SET body=json_set(body,'$.status','identity_unverified','$.directory_id','legacy-device-inode') WHERE task_id='legacy'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.prepareTaskWorkspace(ctx, l, MachinePolicy{ExecutionAccess: "allow"}); err == nil {
		t.Fatal("legacy continuation rebound to the current path")
	}
	if _, err := s.taskWorkspacePath(ctx, l.TaskID); err == nil {
		t.Fatal("legacy execution root accepted")
	}
	if root, err := s.openTaskWorkspace(ctx, l.TaskID); err == nil {
		root.Close()
		t.Fatal("legacy download root accepted")
	}
	if path := bindTestWorkspace(t, s, "explicit-new-task", w.Path); path != w.Path {
		t.Fatal("explicit directory selection did not retain files", path)
	}
	root, err := s.openTaskWorkspace(ctx, "explicit-new-task")
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	data, err := root.ReadFile("result.txt")
	if err != nil || string(data) != "original work" {
		t.Fatal("original workspace files changed", string(data), err)
	}
}

func applyWorkspacePatch(path string) (string, error) {
	return executor.ApplyPatch(path, "*** Begin Patch\n*** Add File: patched.txt\n+proof\n*** End Patch\n")
}
