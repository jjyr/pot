package control

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
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
		peer, err := s.Machines.Peer(target.RuntimeID)
		if err != nil {
			return protocol.FileChunk{}, err
		}
		var chunk protocol.FileChunk
		err = s.remoteCall(ctx, peer, binding(*r.Launch), "task.file.chunk", "file:"+storage.ID(), protocol.FileQuery{Path: path, Offset: offset, Revision: revision}, &chunk)
		if err != nil {
			return chunk, err
		}
		if len(chunk.Data) > int((TaskFileChunkSize+2)/3*4) {
			return protocol.FileChunk{}, errors.New("original Runner file chunk exceeds limit")
		}
		data, decodeErr := base64.StdEncoding.DecodeString(chunk.Data)
		if decodeErr != nil || chunk.Offset != offset || chunk.Size > TaskFileSizeLimit || offset > chunk.Size || uint64(len(data)) > TaskFileChunkSize || offset+uint64(len(data)) > chunk.Size || chunk.Revision == "" || (revision != "" && revision != chunk.Revision) || (offset < chunk.Size && len(data) == 0) {
			return protocol.FileChunk{}, errors.New("invalid original Runner file chunk")
		}
		return chunk, nil
	}
	return s.localTaskFileChunk(ctx, target, task, path, offset, revision)
}
func (s *Service) localTaskFileChunk(ctx context.Context, target core.ExecutionTarget, task, path string, offset uint64, revision string) (protocol.FileChunk, error) {
	var result protocol.FileChunk
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if !protocol.ValidFilePath(path) || offset > TaskFileSizeLimit || len(revision) > 128 {
		return result, errors.New("invalid Task file query")
	}
	base := s.options.Workspace

	anchor, err := os.OpenRoot(base)
	if err != nil {
		return result, err
	}
	defer anchor.Close()
	root, err := anchor.OpenRoot(filepath.Join("tasks", task))
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
