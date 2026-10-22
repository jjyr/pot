package control

import (
	"context"
	"encoding/base64"
	"errors"
	"io"

	"github.com/jjyr/pot/pkg/protocol"
)

const TaskFileChunkSize uint64 = 192 * 1024
const TaskFileSizeLimit uint64 = 1024 * 1024 * 1024

// ReadTaskFileChunk always addresses the persisted original launch, including
// terminal runs. A revision binds identity and mutation timestamps, not a costly
// whole-file digest on every chunk.
func (s *Service) ReadTaskFileChunk(ctx context.Context, owner, task, path string, offset uint64, revision string) (protocol.FileChunk, error) {
	r, err := s.Tasks.Get(ctx, owner, task)
	if err != nil {
		return protocol.FileChunk{}, err
	}
	if r == nil || r.Launch == nil {
		return protocol.FileChunk{}, errors.New("Task has no fixed execution target")
	}
	target := r.Launch.ExecutionTarget
	if target.RuntimeID != s.options.RuntimeID {
		return protocol.FileChunk{}, protocol.APIError{Code: "original_execution_unavailable"}
	}
	return s.localTaskFileChunk(ctx, task, path, offset, revision)
}
func (s *Service) localTaskFileChunk(ctx context.Context, task, path string, offset uint64, revision string) (protocol.FileChunk, error) {
	var result protocol.FileChunk
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if !protocol.ValidFilePath(path) || offset > TaskFileSizeLimit || len(revision) > 128 {
		return result, errors.New("invalid Task file query")
	}
	root, err := s.openTaskWorkspace(ctx, task)
	if err != nil {
		return result, err
	}
	defer root.Close()
	file, err := root.Open(path)
	if err != nil {
		return result, err
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil {
		return result, err
	}
	if !before.Mode().IsRegular() || before.Size() < 0 || uint64(before.Size()) > TaskFileSizeLimit {
		return result, errors.New("Task download requires a regular file at most 1 GiB")
	}
	current, err := taskFileRevision(before)
	if err != nil {
		return result, err
	}
	if revision != "" && revision != current {
		return result, errors.New("Task file changed between chunks")
	}
	if offset > uint64(before.Size()) {
		return result, errors.New("Task file offset exceeds size")
	}
	data := make([]byte, min(TaskFileChunkSize, uint64(before.Size())-offset))
	n, err := file.ReadAt(data, int64(offset))
	if err != nil && err != io.EOF {
		return result, err
	}
	if n != len(data) {
		return result, errors.New("Task file changed during read")
	}
	after, err := file.Stat()
	if err != nil {
		return result, err
	}
	next, err := taskFileRevision(after)
	if err != nil {
		return result, err
	}
	if next != current {
		return result, errors.New("Task file changed during read")
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	return protocol.FileChunk{Size: uint64(before.Size()), Offset: offset, Revision: current, Data: base64.StdEncoding.EncodeToString(data)}, nil
}
