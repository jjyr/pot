//go:build darwin

package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/internal/taskdownload"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func (s *Service) DownloadFile(ctx context.Context, task, path, owner, space string) (bool, error) {
	if !core.Identifier(task) || !core.Identifier(owner) || !protocol.ValidFilePath(path) {
		return false, errors.New("invalid Task download")
	}
	// Capture Space and assistant once, even if the UI selection changes while the
	// dialog or a remote transfer is in progress.
	if _, err := s.spaces.Space(space); err != nil {
		return false, err
	}
	read := func(ctx context.Context, offset uint64, revision string) (protocol.FileChunk, error) {
		query := url.Values{"assistant_id": {owner}, "path": {path}, "offset": {strconv.FormatUint(offset, 10)}, "revision": {revision}}
		data, err := s.spaces.Call(ctx, space, "GET", "/api/v1/tasks/"+task+"/file?"+query.Encode(), nil)
		var c protocol.FileChunk
		if err == nil {
			err = json.Unmarshal(data, &c)
		}
		return c, err
	}
	return s.saveDownload(ctx, filepath.Base(path), read)
}

// DownloadAttachment transfers bytes from the captured Space through its
// authenticated API and uses the native save dialog; WKWebView does not own a
// browser download manager.
func (s *Service) DownloadAttachment(ctx context.Context, id, name, owner, space string) (bool, error) {
	if !core.Identifier(id) || !core.Identifier(owner) || name == "" || strings.ContainsAny(name, "/\\") || len(name) > 255 {
		return false, errors.New("invalid attachment download")
	}
	if _, err := s.spaces.Space(space); err != nil {
		return false, err
	}
	read := func(ctx context.Context, offset uint64, revision string) (protocol.FileChunk, error) {
		query := url.Values{"assistant_id": {owner}, "offset": {strconv.FormatUint(offset, 10)}}
		raw, err := s.spaces.Call(ctx, space, "GET", "/api/v1/attachments/"+id+"?"+query.Encode(), nil)
		var chunk protocol.FileChunk
		if err == nil {
			err = json.Unmarshal(raw, &chunk)
		}
		if err == nil && (chunk.Size > core.AttachmentBytes || chunk.Revision != id) {
			err = errors.New("invalid attachment chunk identity or size")
		}
		return chunk, err
	}
	return s.saveDownload(ctx, name, read)
}
func (s *Service) saveDownload(ctx context.Context, name string, read taskdownload.Read) (bool, error) {
	first, err := read(ctx, 0, "")
	if err != nil {
		return false, err
	}
	if _, err = taskdownload.Validate(first, 0, "", 0); err != nil {
		return false, err
	}
	destination, err := s.app.Dialog.SaveFile().SetFilename(name).CanCreateDirectories(true).AttachToWindow(s.main).PromptForSingleSelection()
	if err != nil || destination == "" {
		return false, err
	}
	file, err := os.CreateTemp(filepath.Dir(destination), ".pot-download-*")
	if err != nil {
		return false, err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err = taskdownload.Copy(ctx, file, first, read); err != nil {
		return false, err
	}
	if err = file.Sync(); err != nil {
		return false, err
	}
	if err = file.Close(); err != nil {
		return false, err
	}
	if err = ctx.Err(); err != nil {
		return false, err
	}
	if err = os.Rename(file.Name(), destination); err != nil {
		return false, err
	}
	return true, nil
}
