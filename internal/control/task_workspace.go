package control

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/processscope"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jjyr/pot/internal/runner"
	"github.com/jjyr/pot/pkg/sandbox"
)

func pathWithin(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
func (s *Service) workspaceDirectory(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("workspace project must be an absolute destination path")
	}
	dir, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(dir)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("workspace is not a directory")
	}
	root, _ := filepath.Abs(s.options.Root)
	home, _ := os.UserHomeDir()
	managedHome := s.options.WorkspaceHome
	if managedHome == "" {
		managedHome = home
	}
	managed := filepath.Join(managedHome, ".pot", "worktrees")
	if canonical, e := filepath.EvalSymlinks(managed); e == nil {
		managed = canonical
	}
	if dir == filepath.Dir(dir) || dir == home || pathWithin(root, dir) || (pathWithin(dir, root) && !pathWithin(dir, s.options.Workspace) && !pathWithin(dir, managed)) {
		return "", errors.New("workspace overlaps Runtime authority or an unrestricted root")
	}
	return dir, nil
}

type taskGitPolicyKey struct{}
type taskGitCredentialKey struct{}
type taskGitPolicy struct {
	launcher    sandbox.Launcher
	root        string
	read, write []string
	protected   bool
	admit       func() (func(), error)
}

func (s *Service) workspaceGitContext(ctx context.Context, w runner.Workspace, policy MachinePolicy) (context.Context, error) {
	gate := func() (func(), error) {
		if policy.Revision == 0 {
			return func() {}, nil
		}
		return s.executionAdmission(policy.Revision)
	}
	if policy.ExecutionAccess != "ask" {
		return context.WithValue(ctx, taskGitPolicyKey{}, taskGitPolicy{admit: gate}), nil
	}
	g := taskGitPolicy{launcher: sandbox.Launcher{Node: s.options.Node, Directory: s.options.SandboxDirectory, ProtectedPaths: []string{s.options.Root}}, root: filepath.Dir(w.Path), protected: true, admit: gate, read: []string{filepath.Join(w.Source, ".git")}}
	for _, arg := range []string{"--git-dir", "--git-common-dir"} {
		dir, err := gitWorkspace(ctx, w.Source, "rev-parse", "--path-format=absolute", arg)
		if err != nil {
			return ctx, err
		}
		dir, err = s.workspaceDirectory(dir)
		if err != nil {
			return ctx, err
		}
		g.read = append(g.read, dir)
		g.write = append(g.write, dir)
	}
	if w.Remote != "" {
		remote, err := gitWorkspace(ctx, w.Source, "remote", "get-url", w.Remote)
		if err != nil {
			return ctx, err
		}
		if u, e := url.Parse(remote); e == nil && u.Scheme == "file" {
			remote = u.Path
		}
		if !strings.Contains(remote, ":") {
			if !filepath.IsAbs(remote) {
				remote = filepath.Join(w.Source, remote)
			}
			dir, e := s.workspaceDirectory(remote)
			if e == nil {
				g.read = append(g.read, dir)
			}
		}
	}
	return context.WithValue(ctx, taskGitPolicyKey{}, g), nil
}

// gitWorkspace disables repository hooks/fsmonitor; errors never echo URLs or credentials.
func gitWorkspace(ctx context.Context, dir string, args ...string) (string, error) {
	prefix := []string{"-c", "core.hooksPath=" + os.DevNull, "-c", "core.fsmonitor=false"}
	token, _ := ctx.Value(taskGitCredentialKey{}).(string)
	if token != "" {
		prefix = append(prefix, "-c", "credential.helper=", "-c", "credential.https://github.com.helper=!gh auth git-credential")
	}
	prefix = append(prefix, "-C", dir)
	command := exec.CommandContext(ctx, "git", append(prefix, args...)...)
	home, _ := os.UserHomeDir()
	command.Env = sandbox.ReplaceEnvironment(sandbox.Environment(), map[string]string{"HOME": home, "GIT_TERMINAL_PROMPT": "0", "GIT_OPTIONAL_LOCKS": "0"})
	if token != "" {
		command.Env = sandbox.ReplaceEnvironment(command.Env, map[string]string{"GH_TOKEN": token})
	}
	var output strings.Builder
	command.Stdout = &output
	var markers processscope.Markers
	finish := func() error { return nil }
	if policy, ok := ctx.Value(taskGitPolicyKey{}).(taskGitPolicy); ok && policy.protected {
		command.Dir = policy.root
		policy.launcher.Markers = &markers
		var err error
		finish, err = policy.launcher.Prepare(ctx, command, policy.root, policy.read, policy.write, false)
		if err != nil {
			return "", err
		}
	}
	command.Args[0] = command.Path
	binary, err := os.Executable()
	if err != nil {
		_ = finish()
		return "", err
	}
	scope, err := processscope.Prepare(command, []string{binary}, markers)
	if err != nil {
		_ = finish()
		return "", err
	}
	command.Cancel = func() error { return scope.Stop(command.Process.Pid) }
	command.WaitDelay = 5 * time.Second
	release := func() {}
	if policy, ok := ctx.Value(taskGitPolicyKey{}).(taskGitPolicy); ok && policy.admit != nil {
		release, err = policy.admit()
		if err != nil {
			scope.Abort()
			_ = finish()
			return "", err
		}
	}
	err = command.Start()
	release()
	if err != nil {
		scope.Abort()
		_ = finish()
		return "", fmt.Errorf("workspace Git could not start: %w", err)
	}
	started := scope.Started()
	runErr := command.Wait()
	cleanup := scope.Finish()
	if cleanup != nil {
		return "", errors.Join(core.ErrCleanupUnconfirmed, cleanup)
	}
	if err = finish(); err != nil {
		return "", errors.Join(core.ErrCleanupUnconfirmed, err)
	}
	if err = errors.Join(started, runErr); err != nil {
		return "", fmt.Errorf("workspace git %s failed: %w", args[0], err)
	}
	if output.Len() > 1024*1024 {
		return "", errors.New("workspace Git output exceeds limit")
	}
	return strings.TrimSpace(output.String()), nil
}
func (s *Service) prepareTaskWorkspace(ctx context.Context, l runner.Launch, policy MachinePolicy) (result *runner.Workspace, err error) {
	if policy.ExecutionAccess == "deny" {
		return nil, errors.New("workspace preparation blocked by machine policy")
	}
	saved, claim, err := s.RunnerJournal.BeginWorkspace(ctx, l.ControlID, l.TaskID, l.RunID)
	if err != nil {
		return nil, err
	}
	if !claim {
		if saved.Status != "ready" {
			return nil, errors.New("original workspace preparation incomplete or failed; inspect original effects before retrying")
		}
		if canonical, checkErr := s.workspaceDirectory(saved.Path); checkErr != nil || canonical != saved.Path {
			err = errors.Join(checkErr, errors.New("workspace path identity changed"))
			return nil, fmt.Errorf("original Task workspace unavailable: %w", err)
		}
		info, statErr := os.Stat(saved.Path)
		if statErr != nil {
			return nil, statErr
		}
		id, idErr := taskDirectoryIdentity(info)
		if idErr != nil || id != saved.DirectoryID {
			return nil, errors.New("original workspace directory identity changed")
		}
		if saved.Kind == "worktree" {
			branch, e := gitWorkspace(ctx, saved.Path, "symbolic-ref", "--short", "HEAD")
			if e != nil || branch != saved.Branch {
				return nil, errors.New("original managed worktree identity changed")
			}
		}
		return saved, nil
	}
	w := *saved
	w.Kind = l.Workspace.Mode()
	save := func() error { return s.RunnerJournal.SaveWorkspace(ctx, l.ControlID, l.TaskID, l.RunID, w) }
	defer func() {
		if err != nil {
			w.Status = "failed"
			w.Error = err.Error()
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			err = errors.Join(err, s.RunnerJournal.SaveWorkspace(cleanup, l.ControlID, l.TaskID, l.RunID, w))
		}
	}()
	switch w.Kind {
	case "temporary":
		w.Path, err = os.MkdirTemp("", "pot-task-")
		if err != nil {
			return nil, err
		}
		w.Path, err = filepath.EvalSymlinks(w.Path)
		if err != nil {
			return nil, err
		}
		if err = save(); err != nil {
			return nil, err
		}
	case "directory", "worktree":
		w.Source, err = s.workspaceDirectory(l.Workspace.ProjectDir)
		if err != nil {
			return nil, err
		}
		if w.Kind == "directory" {
			w.Path = w.Source
			break
		}
		source, gitErr := gitWorkspace(ctx, w.Source, "rev-parse", "--show-toplevel")
		if gitErr != nil {
			return nil, gitErr
		}
		w.Source, err = s.workspaceDirectory(source)
		if err != nil {
			return nil, err
		}
		home := s.options.WorkspaceHome
		if home == "" {
			home, err = os.UserHomeDir()
			if err != nil {
				return nil, err
			}
		}
		parent := filepath.Join(home, ".pot", "worktrees")
		if err = os.MkdirAll(parent, 0700); err != nil {
			return nil, err
		}
		parent, err = filepath.EvalSymlinks(parent)
		if err != nil {
			return nil, err
		}
		for attempt := 0; attempt < 256; attempt++ {
			var id [2]byte
			if _, err = rand.Read(id[:]); err != nil {
				return nil, err
			}
			reserved := filepath.Join(parent, hex.EncodeToString(id[:]))
			err = os.Mkdir(reserved, 0700)
			if os.IsExist(err) {
				continue
			}
			if err != nil {
				return nil, err
			}
			w.Path = filepath.Join(reserved, filepath.Base(w.Source))
			break
		}
		if w.Path == "" {
			return nil, errors.New("worktree directory allocation exhausted")
		}
		w.Branch = l.Workspace.Branch
		if w.Branch == "" {
			w.Branch = "codex/task-" + l.TaskID
		}
		if _, err = gitWorkspace(ctx, w.Source, "check-ref-format", "--branch", w.Branch); err != nil {
			return nil, err
		}
		if strings.HasPrefix(w.Branch, "-") {
			return nil, errors.New("invalid feature branch")
		}
		if err = save(); err != nil {
			return nil, err
		}
		if l.Workspace.Commit != "" {
			if len(l.Workspace.Commit) != 40 && len(l.Workspace.Commit) != 64 {
				return nil, errors.New("pinned commit requires a full Git object ID")
			}
			if _, err = hex.DecodeString(l.Workspace.Commit); err != nil {
				return nil, errors.New("invalid pinned commit")
			}
			w.Commit, err = gitWorkspace(ctx, w.Source, "rev-parse", "--verify", l.Workspace.Commit+"^{commit}")
		} else {
			remotes, e := gitWorkspace(ctx, w.Source, "remote")
			if e != nil {
				return nil, e
			}
			names := strings.Fields(remotes)
			w.Remote = l.Workspace.Remote
			if w.Remote == "" {
				for _, name := range names {
					if name == "origin" {
						w.Remote = name
					}
				}
				if w.Remote == "" && len(names) == 1 {
					w.Remote = names[0]
				}
			}
			found := false
			for _, name := range names {
				if name == w.Remote {
					found = true
				}
			}
			if !found || strings.HasPrefix(w.Remote, "-") {
				return nil, errors.New("workspace remote is unavailable or ambiguous")
			}
			remoteURL, remoteErr := gitWorkspace(ctx, w.Source, "remote", "get-url", w.Remote)
			if remoteErr != nil {
				return nil, remoteErr
			}
			if u, e := url.Parse(remoteURL); e == nil && u.Scheme == "https" && u.Hostname() == "github.com" {
				var token string
				if l.ControlID == s.options.RuntimeID {
					scope := "workspace:" + credentialScope(l.TaskID, l.RunID, l.Generation)
					token, _ = s.Integrations.Credential(ctx, scope, "github", false)
					s.Integrations.Forget(scope)
				} else {
					peer, e := s.Machines.ControlPeer()
					if e != nil {
						return nil, e
					}
					var reply struct {
						Token string `json:"token"`
					}
					if e = s.remoteCall(ctx, peer, binding(l), "task.workspace.github", "workspace-github:"+l.RunID, nil, &reply); e == nil {
						token = reply.Token
					}
				}
				if token != "" {
					ctx = context.WithValue(ctx, taskGitCredentialKey{}, token)
				}
			}
			ctx, err = s.workspaceGitContext(ctx, w, policy)
			if err != nil {
				return nil, err
			}
			w.Base = l.Workspace.Base
			if w.Base == "" {
				head, e := gitWorkspace(ctx, w.Source, "ls-remote", "--symref", w.Remote, "HEAD")
				if e != nil {
					return nil, e
				}
				for _, line := range strings.Split(head, "\n") {
					fields := strings.Fields(line)
					if len(fields) == 3 && fields[0] == "ref:" && fields[2] == "HEAD" {
						w.Base = strings.TrimPrefix(fields[1], "refs/heads/")
					}
				}
			}
			if w.Base == "" {
				return nil, errors.New("remote default base branch unavailable")
			}
			if _, err = gitWorkspace(ctx, w.Source, "check-ref-format", "refs/heads/"+w.Base); err != nil {
				return nil, err
			}

			// A run-specific ref avoids FETCH_HEAD/tracking-ref races between Tasks.
			ref := "refs/pot/tasks/" + l.TaskID + "/" + l.RunID
			if _, err = gitWorkspace(ctx, w.Source, "fetch", "--no-write-fetch-head", "--no-tags", "--no-recurse-submodules", w.Remote, "+refs/heads/"+w.Base+":"+ref); err != nil {
				return nil, err
			}
			w.Commit, err = gitWorkspace(ctx, w.Source, "rev-parse", "--verify", ref+"^{commit}")
		}
		if err != nil {
			return nil, err
		}
		if err = save(); err != nil {
			return nil, err
		}
		ctx = context.WithValue(ctx, taskGitCredentialKey{}, "") // Checkout filters never receive fetch credentials.
		if _, ok := ctx.Value(taskGitPolicyKey{}).(taskGitPolicy); !ok {
			ctx, err = s.workspaceGitContext(ctx, w, policy)
			if err != nil {
				return nil, err
			}
		}
		if _, err = gitWorkspace(ctx, w.Source, "worktree", "add", "-b", w.Branch, "--", w.Path, w.Commit); err != nil {
			return nil, err
		}
	}
	// Git worktree administrative/common metadata are narrowly granted to protected tools.
	if _, e := os.Stat(filepath.Join(w.Path, ".git")); e == nil {
		for _, arg := range []string{"--git-dir", "--git-common-dir"} {
			dir, e := gitWorkspace(ctx, w.Path, "rev-parse", "--path-format=absolute", arg)
			if e != nil {
				return nil, e
			}
			dir, e = filepath.EvalSymlinks(dir)
			if e != nil {
				return nil, e
			}
			root, _ := filepath.Abs(s.options.Root)
			if pathWithin(dir, root) && !pathWithin(dir, s.options.Workspace) {
				return nil, errors.New("Git metadata overlaps Runtime authority")
			}
			w.GitDirs = append(w.GitDirs, dir)
		}
	}
	canonical, pathErr := s.workspaceDirectory(w.Path)
	if pathErr != nil {
		return nil, pathErr
	}
	if canonical != w.Path {
		return nil, errors.New("prepared workspace path identity changed")
	}
	if w.Kind == "worktree" {
		branch, e := gitWorkspace(ctx, w.Path, "symbolic-ref", "--short", "HEAD")
		if e != nil || branch != w.Branch {
			return nil, errors.New("prepared worktree identity changed")
		}
	}
	info, statErr := os.Stat(w.Path)
	if statErr != nil {
		return nil, statErr
	}
	w.DirectoryID, err = taskDirectoryIdentity(info)
	if err != nil {
		return nil, err
	}
	w.Status = "ready"
	if err = save(); err != nil {
		return nil, err
	}
	return &w, nil
}
func (s *Service) taskWorkspacePath(ctx context.Context, task string) (string, error) {
	w, err := s.RunnerJournal.TaskWorkspace(ctx, task)
	if err != nil {
		return "", err
	}
	if w == nil {
		return "", errors.New("original Task workspace is not prepared")
	}
	canonical, err := s.workspaceDirectory(w.Path)
	if err != nil {
		return "", err
	}
	if canonical != w.Path {
		return "", errors.New("workspace path identity changed")
	}
	return canonical, nil
}

// Open and verify the directory handle itself so download roots cannot be swapped
// between path validation and opening (including a symlink to Runtime authority).
func (s *Service) openTaskWorkspace(ctx context.Context, task string) (*os.Root, error) {
	w, err := s.RunnerJournal.TaskWorkspace(ctx, task)
	if err != nil {
		return nil, err
	}
	if w == nil {
		return nil, errors.New("original Task workspace is not prepared")
	}
	canonical, err := s.workspaceDirectory(w.Path)
	if err != nil {
		return nil, err
	}
	if canonical != w.Path {
		return nil, errors.New("workspace path identity changed")
	}
	root, err := os.OpenRoot(w.Path)
	if err != nil {
		return nil, err
	}
	info, err := root.Stat(".")
	if err == nil {
		var id string
		id, err = taskDirectoryIdentity(info)
		if err == nil && id != w.DirectoryID {
			err = errors.New("workspace directory identity changed")
		}
	}
	if err != nil {
		root.Close()
		return nil, err
	}
	return root, nil
}
