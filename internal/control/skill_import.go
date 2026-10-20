package control

import (
	"archive/zip"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/jjyr/pot/pkg/core"
)

// downloadSkill extracts only a selected public GitHub tree, without running
// repository code. Local and downloaded bundles share the same validator.
func downloadSkill(ctx context.Context, source string) (string, func(), error) {
	u, err := url.Parse(source)
	if err != nil || u.Scheme != "https" || u.Host != "github.com" || u.RawQuery != "" || u.Fragment != "" {
		return "", nil, errors.New("expected a public GitHub tree URL")
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 5 || parts[2] != "tree" {
		return "", nil, errors.New("expected GitHub owner/repo/tree/ref/skill-path")
	}
	selected := strings.Join(parts[4:], "/")
	if !core.SafeRelativePath(selected) || parts[0] == "" || parts[1] == "" {
		return "", nil, errors.New("invalid GitHub source")
	}
	directory, err := os.MkdirTemp("", "pot-skill-download-")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(directory) }
	ok := false
	defer func() {
		if !ok {
			cleanup()
		}
	}()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://codeload.github.com/"+url.PathEscape(parts[0])+"/"+url.PathEscape(parts[1])+"/zip/"+url.PathEscape(parts[3]), nil)
	if err != nil {
		return "", nil, err
	}
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("unexpected archive redirect") }}
	response, err := client.Do(request)
	if err != nil {
		return "", nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return "", nil, errors.New("GitHub skill source unavailable")
	}
	file, err := os.Create(filepath.Join(directory, "source.zip"))
	if err != nil {
		return "", nil, err
	}
	n, err := io.Copy(file, io.LimitReader(response.Body, 32*1024*1024+1))
	err = errors.Join(err, file.Close())
	if err != nil {
		return "", nil, err
	}
	if n > 32*1024*1024 {
		return "", nil, errors.New("archive size limit")
	}
	archive, err := zip.OpenReader(file.Name())
	if err != nil {
		return "", nil, err
	}
	defer archive.Close()
	target := filepath.Join(directory, path.Base(selected))
	found := false
	total, count := int64(0), 0
	for _, entry := range archive.File {
		_, relative, valid := strings.Cut(entry.Name, "/")
		if !valid || !strings.HasPrefix(relative, selected+"/") {
			continue
		}
		relative = strings.TrimPrefix(relative, selected+"/")
		relative = strings.TrimSuffix(relative, "/")
		if relative == "" {
			continue
		}
		if !core.SafeRelativePath(relative) || entry.Mode()&os.ModeSymlink != 0 || !entry.Mode().IsRegular() && !entry.FileInfo().IsDir() {
			return "", nil, errors.New("unsafe skill archive")
		}
		dest := filepath.Join(target, filepath.FromSlash(relative))
		if entry.FileInfo().IsDir() {
			if err = os.MkdirAll(dest, 0700); err != nil {
				return "", nil, err
			}
			continue
		}
		count++
		total += int64(entry.UncompressedSize64)
		if count > 2048 || entry.UncompressedSize64 > 2*1024*1024 || total > 16*1024*1024 {
			return "", nil, errors.New("skill size limit")
		}
		if err = os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
			return "", nil, err
		}
		input, err := entry.Open()
		if err != nil {
			return "", nil, err
		}
		data, err := io.ReadAll(io.LimitReader(input, 2*1024*1024+1))
		_ = input.Close()
		if err != nil {
			return "", nil, err
		}
		mode := os.FileMode(0600)
		if entry.Mode()&0111 != 0 {
			mode = 0700
		}
		if err = os.WriteFile(dest, data, mode); err != nil {
			return "", nil, err
		}
		found = true
	}
	if !found {
		return "", nil, errors.New("skill directory missing from archive")
	}
	ok = true
	return target, cleanup, nil
}
