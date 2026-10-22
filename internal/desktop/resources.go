//go:build darwin || (linux && desktop)

package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jjyr/pot/internal/resources"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

func (s *Service) resourceAssets(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "frame-src 'self' blob:; object-src 'none'")
		if r.URL.Path != "/pot-resource-content" {
			next.ServeHTTP(w, r)
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			w.WriteHeader(405)
			return
		}
		id, owner, space, path := r.URL.Query().Get("id"), r.URL.Query().Get("assistant_id"), r.URL.Query().Get("space_id"), r.URL.Query().Get("path")
		if !core.Identifier(id) || !core.Identifier(owner) {
			w.WriteHeader(400)
			return
		}
		query := url.Values{"assistant_id": {owner}}
		data, err := s.spaces.Call(r.Context(), space, "GET", "/api/v1/resources/"+id+"?"+query.Encode(), nil)
		var resource resources.Resource
		if err != nil || json.Unmarshal(data, &resource) != nil {
			w.WriteHeader(404)
			return
		}
		if path == "" {
			path = resource.Entry
		}
		if r.URL.Query().Get("render") == "1" {
			source, err := resources.RenderHTML(resource, func(path string) ([]byte, error) {
				var file *resources.File
				for _, item := range resource.Files {
					if item.Path == path {
						file = &item
						break
					}
				}
				if file == nil {
					return nil, errors.New("missing resource asset")
				}
				reader := &resources.Reader{File: *file, ReadChunk: func(offset uint64) (protocol.FileChunk, error) {
					return s.readResourceChunk(r.Context(), id, owner, space, path, offset)
				}}
				return io.ReadAll(io.LimitReader(reader, resources.BundleLimit+1))
			}, r.URL.Query().Get("token"))
			if err != nil {
				w.WriteHeader(422)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Content-Security-Policy", resources.HTMLPolicy)
			_, _ = w.Write(source)
			return
		}
		var file *resources.File
		for _, item := range resource.Files {
			if item.Path == path {
				file = &item
				break
			}
		}
		if file == nil {
			w.WriteHeader(404)
			return
		}
		read := func(offset uint64) (protocol.FileChunk, error) {
			return s.readResourceChunk(r.Context(), id, owner, space, path, offset)
		}
		contentType := file.MIMEType
		if contentType == "text/html" || contentType == "text/css" || contentType == "text/javascript" {
			contentType = "application/octet-stream"
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("ETag", `"`+file.Digest+`"`)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
		w.Header().Set("Cache-Control", "no-store")
		http.ServeContent(w, r, file.Path, time.Time{}, &resources.Reader{File: *file, ReadChunk: read})
	})
}
func (s *Service) readResourceChunk(ctx context.Context, id, owner, space, path string, offset uint64) (protocol.FileChunk, error) {
	query := url.Values{"assistant_id": {owner}, "path": {path}, "offset": {strconv.FormatUint(offset, 10)}}
	raw, err := s.spaces.Call(ctx, space, "GET", "/api/v1/resources/"+id+"/chunk?"+query.Encode(), nil)
	var chunk protocol.FileChunk
	if err == nil {
		err = json.Unmarshal(raw, &chunk)
	}
	return chunk, err
}
func (s *Service) DownloadResource(ctx context.Context, id, name, owner, space, path string) (bool, error) {
	if !core.Identifier(id) || !core.Identifier(owner) || name == "" || len(name) > 255 || strings.ContainsAny(name, "/\\") || !protocol.ValidFilePath(path) {
		return false, errors.New("invalid resource download")
	}
	return s.saveDownload(ctx, name, func(ctx context.Context, offset uint64, revision string) (protocol.FileChunk, error) {
		return s.readResourceChunk(ctx, id, owner, space, path, offset)
	})
}
