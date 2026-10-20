package transport

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jjyr/pot/internal/resources"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

func resourceRoute(path string) (string, string, bool) {
	if !strings.HasPrefix(path, "/api/v1/resources/") {
		return "", "", false
	}
	parts := strings.Split(strings.TrimPrefix(path, "/api/v1/resources/"), "/")
	if len(parts) > 2 || !core.Identifier(parts[0]) {
		return "", "", false
	}
	if len(parts) == 1 {
		return parts[0], "", true
	}
	return parts[0], parts[1], parts[1] == "chunk" || parts[1] == "content" || parts[1] == "download" || parts[1] == "render"
}
func (a *Application) resourceCall(ctx context.Context, method string, u *url.URL, owner string) (any, error, bool) {
	id, operation, ok := resourceRoute(u.Path)
	if !ok {
		return nil, nil, false
	}
	if method != "GET" {
		return nil, errors.New("GET required"), true
	}
	if operation == "" {
		value, err := a.Control.Resources.Get(ctx, owner, id)
		return value, err, true
	}
	if operation == "chunk" {
		offset := uint64(0)
		if raw := u.Query().Get("offset"); raw != "" {
			var err error
			offset, err = strconv.ParseUint(raw, 10, 64)
			if err != nil {
				return nil, err, true
			}
		}
		value, err := a.Control.Resources.Chunk(ctx, owner, id, u.Query().Get("path"), offset)
		return value, err, true
	}
	return nil, errors.New("resource content requires streaming transport"), true
}
func (a *Application) resourceContent(w http.ResponseWriter, r *http.Request) bool {
	id, operation, ok := resourceRoute(r.URL.Path)
	if !ok || operation != "content" && operation != "download" && operation != "render" {
		return false
	}
	if r.Method != "GET" && r.Method != "HEAD" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return true
	}
	owner := r.URL.Query().Get("assistant_id")
	if owner == "" {
		owner = "main"
	}
	if operation == "render" {
		resource, err := a.Control.Resources.Get(r.Context(), owner, id)
		if err != nil {
			w.WriteHeader(404)
			return true
		}
		source, err := resources.RenderHTML(resource, func(path string) ([]byte, error) {
			f, _, err := a.Control.Resources.OpenFile(r.Context(), owner, id, path)
			if err != nil {
				return nil, err
			}
			defer f.Close()
			return io.ReadAll(io.LimitReader(f, resources.BundleLimit+1))
		}, r.URL.Query().Get("token"))
		if err != nil {
			w.WriteHeader(422)
			return true
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", resources.HTMLPolicy)
		_, _ = w.Write(source)
		return true
	}
	path := r.URL.Query().Get("path")
	if operation == "download" {
		path = "@download"
	}
	file, metadata, err := a.Control.Resources.OpenFile(r.Context(), owner, id, path)
	if err != nil {
		writeError(w, 404, protocol.APIError{Code: "not_found", Message: "resource unavailable"})
		return true
	}
	defer file.Close()
	// HTML is executable only through the separate sandboxed render document.
	contentType := metadata.MIMEType
	if contentType == "text/html" || contentType == "text/javascript" || contentType == "text/css" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("ETag", `"`+metadata.Digest+`"`)
	if operation == "download" {
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": metadata.Path[strings.LastIndex(metadata.Path, "/")+1:]}))
	}
	http.ServeContent(w, r, metadata.Path, time.Time{}, file)
	return true
}
