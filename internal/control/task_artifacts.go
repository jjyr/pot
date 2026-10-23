package control

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"

	"github.com/jjyr/pot/internal/tasks"
	"github.com/jjyr/pot/pkg/protocol"
)

const taskArtifactsDirectory = ".pot-artifacts"
const taskArtifactsFileLimit = 64
const taskArtifactsEntryLimit = 256
const taskArtifactsSizeLimit int64 = 64 * 1024 * 1024

// Artifact preparation never changes execution status or the final reply. Its
// metadata commits with the same terminal outcome and schedule history.
func (s *Service) prepareTaskOutcome(ctx context.Context, task tasks.Record, event protocol.TaskResultEvent) (tasks.OutcomeCommit, error) {
	commit, err := s.Schedules.PrepareOutcome(ctx, task, event)
	if err != nil {
		return nil, err
	}
	var artifacts []string
	var artifactErr error
	if task.Launch != nil {
		if task.Launch.ExecutionTarget.RuntimeID != s.options.RuntimeID {
			artifactErr = protocol.APIError{Code: "original_execution_unavailable"}
		} else {
			workspace, err := s.RunJournal.TaskWorkspace(ctx, task.Info.ID)
			if err != nil {
				artifactErr = err
			} else if workspace != nil {
				root, err := s.openTaskWorkspace(ctx, task.Info.ID)
				if err != nil {
					artifactErr = err
				} else {
					artifacts, artifactErr = collectTaskArtifacts(ctx, root)
					root.Close()
				}
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return func(ctx context.Context, tx *sql.Tx, event *protocol.TaskResultEvent) error {
		event.Artifacts = artifacts
		if artifactErr != nil {
			event.ArtifactsError = artifactErr.Error()
		}
		if commit != nil {
			return commit(ctx, tx, event)
		}
		return nil
	}, nil
}

// Collect paths, not bytes. Reads and later downloads stay bound to the original
// workspace. Symlinks and special files are never delivered automatically.
func collectTaskArtifacts(ctx context.Context, workspace *os.Root) ([]string, error) {
	info, err := workspace.Lstat(taskArtifactsDirectory)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%s must be an ordinary directory", taskArtifactsDirectory)
	}
	root, err := workspace.OpenRoot(taskArtifactsDirectory)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	var files []string
	var diagnostics []error
	entries, total := 0, int64(0)
	var walk func(string, int)
	walk = func(directory string, depth int) {
		if err := ctx.Err(); err != nil {
			diagnostics = append(diagnostics, err)
			return
		}
		if depth > 16 {
			diagnostics = append(diagnostics, fmt.Errorf("artifact directory depth exceeds 16: %s", directory))
			return
		}
		dir, err := root.Open(directory)
		if err != nil {
			diagnostics = append(diagnostics, err)
			return
		}
		children, err := dir.ReadDir(taskArtifactsEntryLimit - entries + 1)
		dir.Close()
		if err != nil && !errors.Is(err, io.EOF) {
			diagnostics = append(diagnostics, err)
			return
		}
		sort.Slice(children, func(i, j int) bool { return children[i].Name() < children[j].Name() })
		for _, child := range children {
			entries++
			if entries > taskArtifactsEntryLimit {
				diagnostics = append(diagnostics, fmt.Errorf("artifact directory exceeds %d entries", taskArtifactsEntryLimit))
				return
			}
			name := path.Join(directory, child.Name())
			filePath := path.Join(taskArtifactsDirectory, name)
			info, err := root.Lstat(name)
			if err != nil {
				diagnostics = append(diagnostics, err)
				continue
			}
			if info.IsDir() {
				walk(name, depth+1)
			} else if !info.Mode().IsRegular() || !protocol.ValidFilePath(filePath) || len(filePath) > 1000 {
				diagnostics = append(diagnostics, fmt.Errorf("unsupported artifact: %s", filePath))
			} else if len(files) >= taskArtifactsFileLimit || info.Size() < 0 || info.Size() > taskArtifactsSizeLimit-total {
				diagnostics = append(diagnostics, fmt.Errorf("artifact exceeds delivery count or size limit: %s", filePath))
			} else {
				total += info.Size()
				files = append(files, filePath)
			}
			if entries > taskArtifactsEntryLimit || ctx.Err() != nil {
				return
			}
		}
	}
	walk(".", 0)
	sort.Strings(files)
	return files, errors.Join(diagnostics...)
}
