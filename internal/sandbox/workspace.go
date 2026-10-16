package sandbox

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/jjyr/pot/internal/core"
)

// ErrOutsideWorkspace identifies a guest symlink which cannot be an instruction
// source. Callers omit that source with a diagnostic; other I/O errors propagate.
var ErrOutsideWorkspace = errors.New("instruction path outside Sandbox workspace mount")

type WorkspacePaths struct {
	HostRoot, HostCWD   string
	GuestRoot, GuestCWD string
}

// WorkspacePaths does no guest I/O, starts no VM and creates no directories.
func (m *Manager) WorkspacePaths(ctx context.Context, target core.ExecutionTarget, owner string, worker bool) (WorkspacePaths, error) {
	if err := target.Validate(); err != nil {
		return WorkspacePaths{}, err
	}
	if !target.IsLocalTo(m.runtimeID) || target.Target.Kind != "sandbox" {
		return WorkspacePaths{}, errors.New("workspace belongs to another Runtime or target")
	}
	if worker && (!core.Identifier(owner) || strings.ContainsAny(owner, "/\\")) {
		return WorkspacePaths{}, errors.New("invalid Task workspace owner")
	}
	_, provider, instance, err := m.provider(ctx, target.Target.SandboxID)
	if err != nil {
		return WorkspacePaths{}, err
	}
	value := WorkspacePaths{HostRoot: provider.Workspace(instance), GuestRoot: provider.GuestWorkspace()}
	value.HostCWD, value.GuestCWD = value.HostRoot, value.GuestRoot
	if worker {
		value.HostCWD = filepath.Join(value.HostRoot, "tasks", owner)
		value.GuestCWD = path.Join(value.GuestRoot, "tasks", owner)
	}
	return value, nil
}

// InstructionFS reads only the Host-shared workspace. Execution-visible absolute
// symlinks resolve in guest space. os.Root also confines races with Host writers.
type InstructionFS struct {
	root      *os.Root
	guestRoot string
}

func (w WorkspacePaths) OpenInstructions() (*InstructionFS, error) {
	root, err := os.OpenRoot(w.HostRoot)
	if err != nil {
		return nil, err
	}
	if !path.IsAbs(w.GuestRoot) || path.Clean(w.GuestRoot) != w.GuestRoot {
		root.Close()
		return nil, errors.New("invalid guest workspace mount")
	}
	return &InstructionFS{root: root, guestRoot: w.GuestRoot}, nil
}
func (f *InstructionFS) Close() error { return f.root.Close() }
func (f *InstructionFS) relative(value string) (string, error) {
	value = path.Clean(value)
	if value == f.guestRoot {
		return ".", nil
	}
	if !strings.HasPrefix(value, f.guestRoot+"/") {
		return "", ErrOutsideWorkspace
	}
	return strings.TrimPrefix(value, f.guestRoot+"/"), nil
}
func (f *InstructionFS) resolve(value string) (string, error) {
	if !path.IsAbs(value) {
		return "", ErrOutsideWorkspace
	}
	relative, err := f.relative(value)
	if err != nil {
		return "", err
	}
	links := 0
	for {
		parts := strings.Split(relative, "/")
		restart := false
		for i := range parts {
			prefix := path.Join(parts[:i+1]...)
			info, err := f.root.Lstat(filepath.FromSlash(prefix))
			if err != nil {
				return "", err
			}
			if info.Mode()&os.ModeSymlink == 0 {
				continue
			}
			links++
			if links > 40 {
				return "", &fs.PathError{Op: "resolve", Path: value, Err: errors.New("too many symlinks")}
			}
			target, err := f.root.Readlink(filepath.FromSlash(prefix))
			if err != nil {
				return "", err
			}
			if !path.IsAbs(target) {
				target = path.Join(f.guestRoot, path.Dir(prefix), filepath.ToSlash(target))
			}
			target = path.Join(append([]string{target}, parts[i+1:]...)...)
			relative, err = f.relative(target)
			if err != nil {
				return "", err
			}
			restart = true
			break
		}
		if !restart {
			return filepath.FromSlash(relative), nil
		}
	}
}
func (f *InstructionFS) Stat(value string) (fs.FileInfo, error) {
	relative, err := f.resolve(value)
	if err != nil {
		return nil, err
	}
	return f.root.Stat(relative)
}
func (f *InstructionFS) ReadFile(value string) ([]byte, error) {
	relative, err := f.resolve(value)
	if err != nil {
		return nil, err
	}
	return f.root.ReadFile(relative)
}

// SourcePath maps a resolved execution-visible path for project Skill discovery.
// Readers needing race-safe confinement must continue using this InstructionFS.
func (f *InstructionFS) SourcePath(value string) (string, error) {
	relative, err := f.resolve(value)
	if err != nil {
		return "", err
	}
	return filepath.Join(f.root.Name(), relative), nil
}
