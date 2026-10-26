package control

import (
	"context"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/jjyr/pot/internal/resources"
	"github.com/jjyr/pot/pkg/protocol"
)

var fileLine = regexp.MustCompile(`:([1-9][0-9]*)$`)
var errFileForbidden = errors.New("file reference forbidden")
var errFileTooLarge = errors.New("file reference too large")
var errFileChanged = errors.New("file reference changed")

// messageFileCompletion freezes the host execution identity before model/tool
// execution. A later directory replacement cannot become a newly allowed root.
func (s *Service) messageFileCompletion(owner, task, run, root string) func(string, string) {
	source := resources.Source{Node: s.options.RuntimeID, Owner: owner, Task: task, Run: run, Root: root}
	if info, err := os.Lstat(root); err == nil && info.IsDir() {
		source.RootID, _ = taskDirectoryIdentity(info)
	}
	if task == "" {
		if info, err := os.Lstat(s.Registry.Directory(owner)); err == nil && info.IsDir() {
			source.ConfigID, _ = taskDirectoryIdentity(info)
		}
		source.Config = filepath.Join(s.Registry.Directory(owner), "AGENTS.md")
	}
	return func(message, text string) { s.bindMessageFileSource(source, message, text) }
}

// Projection failure never fails a model reply or feeds file bytes to the model.
func (s *Service) bindMessageFileSource(source resources.Source, message, text string) {
	owner, task := source.Owner, source.Task
	conversation := "main"
	if task != "" {
		conversation = "task:" + task
	}
	_ = s.Resources.BindMessageSources(s.ctx, owner, conversation, message, text, source, func(link resources.Link) resources.Source {
		if !strings.HasPrefix(link.Href, "pot-file://") {
			return source
		}
		u, err := url.Parse(link.Href)
		if err != nil {
			return source
		}
		if task != "" {
			return source
		}
		denied := resources.Source{Node: source.Node, Owner: owner, Task: "forbidden"}
		r, err := s.Tasks.Get(s.ctx, owner, u.Host)
		if err != nil || r == nil {
			return denied
		}
		bound := resources.Source{Owner: owner, Task: u.Host}
		if r.Launch == nil {
			return bound
		}
		bound.Node = r.Launch.ExecutionTarget.RuntimeID
		bound.Run = r.Launch.RunID
		if bound.Node == s.options.RuntimeID {
			if workspace, err := s.RunJournal.TaskWorkspace(s.ctx, u.Host); err == nil && workspace != nil {
				bound.Root = workspace.Path
				bound.RootID = workspace.DirectoryID
			}
		}
		return bound
	})
}
func (s *Service) messageFileLoop() {
	defer s.fileWG.Done()
	timer := time.NewTicker(250 * time.Millisecond)
	defer timer.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-timer.C:
		}
		refs, err := s.Resources.PendingFiles(s.ctx)
		if err != nil {
			continue
		}
		for _, ref := range refs {
			if s.ctx.Err() != nil {
				return
			}
			s.resolveMessageFile(s.ctx, ref)
		}
	}
}
func (s *Service) resolveMessageFile(ctx context.Context, ref resources.MessageFile) {
	_ = s.Resources.FileState(ctx, ref.ID, "resolving", nil)
	resource, err := s.snapshotMessageFile(ctx, ref)
	state := "ready"
	if err != nil {
		state = "unavailable"
		switch {
		case errors.Is(err, errFileForbidden), errors.Is(err, os.ErrPermission), strings.Contains(err.Error(), "escapes from parent"), errors.Is(err, syscall.ELOOP):
			state = "forbidden"
		case errors.Is(err, os.ErrNotExist):
			state = "not_found"
		case errors.Is(err, errFileTooLarge):
			state = "too_large"
		case errors.Is(err, errFileChanged):
			state = "changed"
		case errors.Is(err, resources.ErrDependency):
			state = "unsupported_dependency"
		default:
			var api protocol.APIError
			if errors.As(err, &api) && api.Code == "original_execution_unavailable" {
				state = "source_offline"
			}
		}
		resource = nil
	}
	_ = s.Resources.FileState(ctx, ref.ID, state, resource)
}
func (s *Service) snapshotMessageFile(ctx context.Context, ref resources.MessageFile) (*resources.Resource, error) {
	src := ref.Source
	if src.Node != s.options.RuntimeID {
		return nil, protocol.APIError{Code: "original_execution_unavailable"}
	}
	if err := s.Resources.ValidateOwner(ctx, src.Owner); err != nil {
		return nil, errFileForbidden
	}
	return s.snapshotLocalMessageFile(ctx, ref)
}

func (s *Service) registerMessageSnapshot(ctx context.Context, ref resources.MessageFile, request, hash, path string, read func(string, int) ([]byte, error), verify func() error) (*resources.Resource, error) {
	data, err := read(path, resources.FileLimit)
	if err != nil {
		return nil, err
	}
	paths := []string{path}
	contents := [][]byte{data}
	if resources.DetectType(path, data) == "text/html" && !strings.EqualFold(filepath.Ext(path), ".md") && !strings.EqualFold(filepath.Ext(path), ".markdown") {
		if len(data) > resources.BundleLimit {
			return nil, errFileTooLarge
		}
		paths, contents, err = resources.CollectHTML(path, data, read)
		if err != nil {
			return nil, err
		}
	}
	if err := verify(); err != nil {
		return nil, err
	}
	saved, err := s.Resources.Register(ctx, ref.Source.Owner, request, hash, filepath.Base(path), path, paths, contents)
	if err != nil {
		return nil, err
	}
	return &saved, nil
}
func readBoundRoot(ctx context.Context, root *os.Root, path string, limit int, expected string, guards ...func(os.FileInfo) error) ([]byte, error) {
	if limit < 0 {
		return nil, errFileTooLarge
	}
	flags := os.O_RDONLY | syscall.O_NONBLOCK
	file, err := root.OpenFile(path, flags, 0)
	if err != nil {
		if strings.Contains(err.Error(), "escapes from parent") {
			return nil, errFileForbidden
		}
		return nil, err
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, errFileForbidden
	}
	for _, guard := range guards {
		if err := guard(before); err != nil {
			return nil, err
		}
	}
	if before.Size() > int64(limit) {
		return nil, errFileTooLarge
	}
	revision, err := taskFileRevision(before)
	if err != nil {
		return nil, err
	}
	if revision != expected {
		return nil, errFileChanged
	}
	data, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	after, err := file.Stat()
	if err != nil {
		return nil, err
	}
	next, err := taskFileRevision(after)
	if err != nil {
		return nil, err
	}
	if len(data) > limit {
		return nil, errFileTooLarge
	}
	if int64(len(data)) != before.Size() || revision != next {
		return nil, errFileChanged
	}
	return data, ctx.Err()
}
func (s *Service) MessageFiles(ctx context.Context, owner, task, message string) ([]resources.MessageFile, error) {
	conversation := "main"
	if task != "" {
		if record, err := s.Tasks.Get(ctx, owner, task); err != nil || record == nil {
			return nil, errFileForbidden
		}
		conversation = "task:" + task
	}
	return s.Resources.MessageFiles(ctx, owner, conversation, message)
}
func (s *Service) RetryMessageFile(ctx context.Context, owner, task, message, ref string) error {
	if _, err := s.MessageFiles(ctx, owner, task, message); err != nil {
		return err
	}
	conversation := "main"
	if task != "" {
		conversation = "task:" + task
	}
	return s.Resources.RetryFile(ctx, owner, conversation, message, ref)
}
