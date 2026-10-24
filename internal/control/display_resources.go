package control

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/jjyr/pot/internal/resources"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

type resourceSource struct {
	Path   string   `json:"path"`
	TaskID string   `json:"task_id,omitempty"`
	Name   string   `json:"name,omitempty"`
	Assets []string `json:"assets,omitempty"`
}

func (s *Service) registerResource(ctx context.Context, owner, executionOwner, request string, worker bool, args json.RawMessage) (json.RawMessage, error) {
	var source resourceSource
	if err := core.DecodeJSON(args, &source); err != nil {
		return nil, err
	}
	if !protocol.ValidFilePath(source.Path) {
		return nil, errors.New("invalid resource path")
	}
	if len(source.Assets) > 63 {
		return nil, errors.New("resource assets exceeds 63 files")
	}
	if worker {
		if source.TaskID != "" && source.TaskID != executionOwner {
			return nil, errors.New("worker resources require its own Task workspace")
		}
		source.TaskID = executionOwner
	}
	paths := append([]string{source.Path}, source.Assets...)
	seen := map[string]bool{}
	for _, path := range paths {
		if !protocol.ValidFilePath(path) || seen[path] {
			return nil, errors.New("invalid resource assets")
		}
		seen[path] = true
	}
	hash, err := core.HashJSON(source)
	if err != nil {
		return nil, err
	}
	if previous, err := s.Resources.Receipt(ctx, owner, request, hash); err != nil {
		return nil, err
	} else if previous != nil {
		return json.Marshal(previous)
	}
	var contents [][]byte
	var total int
	limit := resources.FileLimit
	if len(paths) > 1 {
		limit = resources.BundleLimit
	}
	for _, path := range paths {
		data, err := s.readResourceSource(ctx, owner, source.TaskID, path, limit-total)
		if err != nil {
			return nil, err
		}
		total += len(data)
		contents = append(contents, data)
	}
	name := source.Name
	if name == "" {
		name = filepath.Base(source.Path)
	}
	saved, err := s.Resources.Register(ctx, owner, request, hash, name, source.Path, paths, contents)
	if err != nil {
		return nil, err
	}
	return json.Marshal(saved)
}
func (s *Service) readResourceSource(ctx context.Context, owner, task, path string, limit int) ([]byte, error) {
	if limit < 0 {
		return nil, errors.New("resource exceeds size limit")
	}
	if task != "" {
		var data []byte
		revision := ""
		for {
			chunk, err := s.ReadTaskFileChunk(ctx, owner, task, path, uint64(len(data)), revision)
			if err != nil {
				return nil, err
			}
			if chunk.Size > uint64(limit) {
				return nil, errors.New("resource exceeds size limit")
			}
			bytes, err := base64.StdEncoding.Strict().DecodeString(chunk.Data)
			if err != nil {
				return nil, err
			}
			data = append(data, bytes...)
			revision = chunk.Revision
			if uint64(len(data)) == chunk.Size {
				return data, nil
			}
		}
	}
	directory, err := s.ExecutionWorkspace(owner)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	file, err := root.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Size() > int64(limit) {
		return nil, errors.New("resource requires a regular file within size limit")
	}
	data, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	after, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if len(data) > limit || int64(len(data)) != before.Size() || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return nil, errors.New("resource source changed during registration")
	}
	return data, ctx.Err()
}
