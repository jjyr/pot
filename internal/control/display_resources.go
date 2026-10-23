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
	"github.com/jjyr/pot/pkg/modelprotocol"
	"github.com/jjyr/pot/pkg/protocol"
)

func resourceTools() []modelprotocol.ToolDefinition {
	return []modelprotocol.ToolDefinition{
		{Name: "register_resource", Description: "Save an immutable display resource from existing workspace files. path is relative to your workspace, or an owned Task workspace when task_id is given. HTML can use up to 63 additional relative assets, resolved from the same workspace. HTML runs isolated with no network, parent DOM, native bridge or tool access; use self-contained JS and CSS. Returns a resource ID, never original bytes. Registration does not display or download. name is a display label; download filenames are derived separately. Maximum 64 MiB per file/resource, 16 MiB HTML bundle. Reuse resource IDs for later displays.", InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","minLength":1,"maxLength":4096},"task_id":{"type":"string","maxLength":128},"name":{"type":"string","description":"Optional display label, preserved verbatim; defaults to the source basename when empty. Download filenames are derived separately."},"assets":{"type":"array","maxItems":63,"items":{"type":"string","minLength":1,"maxLength":4096}}},"required":["path"],"additionalProperties":false}`)},
		{Name: "present_resource", Description: "Show an existing registered resource directly in this conversation. preview displays an image/GIF, audio/video player, PDF/Markdown/text viewer or isolated interactive HTML component; download shows a file delivery card with an explicit save action. Neither mode automatically downloads. UI also offers an independent download action on previews. Use only actual resource IDs returned by register_resource. Do not repeat a shown resource as a download link.", InputSchema: json.RawMessage(`{"type":"object","properties":{"resource_id":{"type":"string","minLength":1,"maxLength":128},"mode":{"type":"string","enum":["preview","download"]},"caption":{"type":"string","maxLength":1000}},"required":["resource_id","mode"],"additionalProperties":false}`)},
	}
}

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
func (s *Service) presentResource(ctx context.Context, owner string, args json.RawMessage) (json.RawMessage, error) {
	var input struct {
		ResourceID string `json:"resource_id"`
		Mode       string `json:"mode"`
		Caption    string `json:"caption"`
	}
	if err := core.DecodeJSON(args, &input); err != nil {
		return nil, err
	}
	if input.Mode != "preview" && input.Mode != "download" || len(input.Caption) > 1000 {
		return nil, errors.New("invalid resource presentation")
	}
	resource, err := s.Resources.Get(ctx, owner, input.ResourceID)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"presentation": resources.Presentation{Resource: resource, Mode: input.Mode, Caption: input.Caption}})
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
