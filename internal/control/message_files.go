package control

import (
	"context"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jjyr/pot/internal/resources"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

var fileLine = regexp.MustCompile(`:([1-9][0-9]*)$`)
var errFileForbidden = errors.New("file reference forbidden")
var errFileTooLarge = errors.New("file reference too large")
var errFileChanged = errors.New("file reference changed")

// bindMessageFiles captures provenance before enqueueing any asynchronous reads.
// Projection failure must never fail a model reply or feed bytes to the model.
func (s *Service) bindMessageFiles(owner, task, run, message, text, root string) {
	source := resources.Source{Node: s.options.RuntimeID, Owner: owner, Task: task, Run: run, Root: root}
	if info, err := os.Stat(root); err == nil {
		source.RootID, _ = taskDirectoryIdentity(info)
	}
	if task == "" {
		if info, err := os.Lstat(s.Registry.Directory(owner)); err == nil && info.IsDir() {
			source.ConfigID, _ = taskDirectoryIdentity(info)
		}
		source.Config = filepath.Join(s.Registry.Directory(owner), "AGENTS.md")
	}
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
	root := src.Root
	rootID := src.RootID
	taskRoot := src.Task
	path := ref.Href
	if match := fileLine.FindStringSubmatchIndex(path); match != nil {
		if _, err := strconv.ParseUint(path[match[2]:match[3]], 10, 32); err != nil {
			return nil, errFileForbidden
		}
		path = path[:match[0]]
	}
	if strings.HasPrefix(path, "pot-file://") {
		u, err := url.Parse(path)
		if err != nil || !core.Identifier(u.Host) || u.RawQuery != "" || u.Fragment != "" {
			return nil, errFileForbidden
		}
		if src.Task == "" || u.Host != src.Task {
			return nil, errFileForbidden
		}
		path = strings.TrimPrefix(u.Path, "/")
	}

	if !strings.HasPrefix(ref.Href, "pot-file://") {
		decoded, err := url.PathUnescape(path)
		if err != nil {
			return nil, errFileForbidden
		}
		path = decoded
	}
	config := false
	if filepath.IsAbs(path) {
		if src.Task == "" && path == src.Config && src.Config == filepath.Join(s.Registry.Directory(src.Owner), "AGENTS.md") {
			root = filepath.Dir(path)
			path = "AGENTS.md"
			config = true
			rootID = src.ConfigID
			info, err := os.Lstat(root)
			if err != nil {
				return nil, err
			}
			if src.ConfigID == "" || !info.IsDir() {
				return nil, errFileForbidden
			}
		} else {
			if !pathWithin(path, root) {
				return nil, errFileForbidden
			}
			var err error
			path, err = filepath.Rel(root, path)
			if err != nil {
				return nil, errFileForbidden
			}
		}
	}
	path = filepath.ToSlash(path)
	if !protocol.ValidFilePath(path) {
		return nil, errFileForbidden
	}
	// Repeated links (including differing line numbers/labels) share one immutable
	// receipt in this message revision. Retrying ready references never rereads.
	hash, _ := core.HashJSON([]any{src, root, path, ref.Message, ref.Revision})
	request := "message-file-" + hash
	if previous, err := s.Resources.Receipt(ctx, src.Owner, request, hash); err != nil {
		return nil, err
	} else if previous != nil {
		return previous, nil
	}
	if taskRoot != "" {
		r, err := s.Tasks.Get(ctx, src.Owner, taskRoot)
		if err != nil || r == nil {
			return nil, errFileForbidden
		}
		original, err := s.Tasks.Run(ctx, src.Owner, taskRoot, src.Run)
		if err != nil || original == nil || original.Launch == nil {
			return nil, protocol.APIError{Code: "original_execution_unavailable"}
		}
		if original.Launch.ExecutionTarget.RuntimeID != src.Node {
			return nil, protocol.APIError{Code: "original_execution_unavailable"}
		}
		workspace, err := s.RunJournal.TaskWorkspace(ctx, taskRoot)
		if err != nil || workspace == nil {
			return nil, protocol.APIError{Code: "original_execution_unavailable"}
		}
		if workspace.Path != root || workspace.DirectoryID != rootID {
			return nil, errFileForbidden
		}
	}
	var handle *os.Root
	var err error
	if taskRoot != "" {
		handle, err = s.openTaskWorkspace(ctx, taskRoot)
	} else {
		handle, err = os.OpenRoot(root)
		if err == nil {
			info, e := handle.Stat(".")
			if e != nil {
				err = e
			} else {
				id, e := taskDirectoryIdentity(info)
				if e != nil || rootID == "" || id != rootID {
					err = errFileForbidden
				}
			}
		}
	}
	if err != nil {
		if handle != nil {
			handle.Close()
		}
		return nil, err
	}
	defer handle.Close()
	stamps := map[string]string{}
	read := func(path string, limit int) ([]byte, error) {
		info, err := handle.Stat(path)
		if err != nil {
			return nil, err
		}
		before, err := taskFileRevision(info)
		if err != nil {
			return nil, err
		}
		data, err := readBoundRoot(ctx, handle, path, limit, config)
		if err != nil {
			return nil, err
		}
		info, err = handle.Stat(path)
		if err != nil {
			return nil, err
		}
		after, err := taskFileRevision(info)
		if err != nil {
			return nil, err
		}
		if before != after {
			return nil, errFileChanged
		}
		stamps[path] = after
		return data, nil
	}
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
	for path, stamp := range stamps {
		info, err := handle.Stat(path)
		if err != nil {
			return nil, err
		}
		current, err := taskFileRevision(info)
		if err != nil {
			return nil, err
		}
		if stamp != current {
			return nil, errFileChanged
		}
	}
	saved, err := s.Resources.Register(ctx, src.Owner, request, hash, filepath.Base(path), path, paths, contents)
	if err != nil {
		return nil, err
	}
	return &saved, nil
}
func readBoundRoot(ctx context.Context, root *os.Root, path string, limit int, config bool) ([]byte, error) {
	if limit < 0 {
		return nil, errFileTooLarge
	}
	if config {
		info, err := root.Lstat(path)
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, errFileForbidden
		}
	}
	flags := os.O_RDONLY | syscall.O_NONBLOCK
	if config {
		flags |= syscall.O_NOFOLLOW
	}
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
	if before.Size() > int64(limit) {
		return nil, errFileTooLarge
	}
	revision, err := taskFileRevision(before)
	if err != nil {
		return nil, err
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
