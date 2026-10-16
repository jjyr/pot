package transport

import (
	"context"
	"github.com/jjyr/pot/internal/taskdownload"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
	"mime"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
)

func (a *Application) download(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != "GET" || !strings.HasPrefix(r.URL.Path, "/api/v1/tasks/") || !strings.HasSuffix(r.URL.Path, "/file-download") {
		return false
	}
	task := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/tasks/"), "/file-download")
	owner := r.URL.Query().Get("assistant_id")
	if owner == "" {
		owner = "main"
	}
	name := r.URL.Query().Get("path")
	if !core.Identifier(task) || !core.Identifier(owner) || !protocol.ValidFilePath(name) {
		writeError(w, 400, protocol.APIError{Code: "invalid_file", Message: "invalid Task download"})
		return true
	}
	read := func(ctx context.Context, offset uint64, revision string) (protocol.FileChunk, error) {
		return a.Control.ReadTaskFileChunk(ctx, owner, task, name, offset, revision)
	}
	first, err := read(r.Context(), 0, "")
	if err == nil {
		_, err = taskdownload.Validate(first, 0, "", 0)
	}
	if err != nil {
		writeError(w, 409, protocol.APIError{Code: "download_failed", Message: err.Error()})
		return true
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filepath.Base(name)}))
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Content-Length", strconv.FormatUint(first.Size, 10))
	if err = taskdownload.Copy(r.Context(), w, first, read); err != nil {
		panic(http.ErrAbortHandler)
	}
	return true
}
