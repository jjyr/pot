// Package resource verifies immutable owner-approved resources. Authorization,
// catalogs and sandbox lifecycle belong to the calling product service.
package resource

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/jjyr/pot/pkg/core"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type Format string

const (
	Raw        Format = "raw"
	OCITarGzip Format = "oci-tar-gzip"
)

type Resource struct {
	ReleaseTag    string `json:"release_tag"`
	Filename      string `json:"filename"`
	SHA256        string `json:"sha256"`
	SizeBytes     uint64 `json:"size_bytes"`
	UnpackedBytes uint64 `json:"unpacked_bytes"`
	Format        Format `json:"format"`
}

func (r Resource) Validate() error {
	for _, name := range []string{r.ReleaseTag, r.Filename} {
		if name == "" || len(name) >= 200 || strings.HasPrefix(name, ".") {
			return errors.New("invalid resource name")
		}
		for _, c := range name {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-_.", c)) {
				return errors.New("invalid resource name")
			}
		}
	}
	digest, err := hex.DecodeString(r.SHA256)
	if err != nil || len(digest) != 32 || strings.ToLower(r.SHA256) != r.SHA256 {
		return errors.New("invalid resource SHA256")
	}
	if r.SizeBytes == 0 || r.UnpackedBytes == 0 || r.SizeBytes > core.MaxSafeInteger || r.UnpackedBytes > core.MaxSafeInteger {
		return errors.New("invalid resource size")
	}
	switch r.Format {
	case Raw:
		if r.SizeBytes != r.UnpackedBytes {
			return errors.New("raw resource size mismatch")
		}
	case OCITarGzip:
	default:
		return errors.New("invalid resource format")
	}
	return nil
}

type Verified struct{ path, digest string }

func (v Verified) Path() string   { return v.path }
func (v Verified) Digest() string { return v.digest }

type Progress struct {
	Phase            string  `json:"phase"`
	DownloadedBytes  uint64  `json:"downloaded_bytes"`
	TotalBytes       uint64  `json:"total_bytes"`
	BytesPerSecond   uint64  `json:"bytes_per_second"`
	RemainingSeconds *uint64 `json:"remaining_seconds"`
}

func regular(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("resource must be a regular file without links")
	}
	return nil
}
func hashFile(ctx context.Context, path string) (string, error) {
	if err := regular(path); err != nil {
		return "", err
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	h := sha256.New()
	if _, err = io.Copy(h, &contextReader{ctx, file}); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func verify(ctx context.Context, path string, r Resource) error {
	if err := r.Validate(); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || uint64(info.Size()) != r.SizeBytes {
		return errors.New("resource size or type mismatch")
	}
	digest, err := hashFile(ctx, path)
	if err != nil {
		return err
	}
	if digest != r.SHA256 {
		return errors.New("resource SHA256 mismatch")
	}
	return nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
func (s *Store) Cached(ctx context.Context, r Resource) (Verified, bool, error) {
	if err := r.Validate(); err != nil {
		return Verified{}, false, err
	}
	dir := filepath.Join(s.root, r.SHA256)
	info, err := os.Lstat(dir)
	if os.IsNotExist(err) {
		return Verified{}, false, nil
	}
	if err != nil {
		return Verified{}, false, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return Verified{}, false, errors.New("cache directory link forbidden")
	}
	blob := filepath.Join(dir, "blob")
	if err = regular(filepath.Join(dir, "complete.json")); err != nil {
		return Verified{}, false, fmt.Errorf("resource cache is incomplete: %w", err)
	}
	if err = verify(ctx, blob, r); err != nil {
		return Verified{}, false, err
	}
	expected, err := archiveFiles(ctx, blob, r, "")
	if err != nil {
		return Verified{}, false, err
	}
	content := filepath.Join(dir, "content")
	rootInfo, err := os.Lstat(content)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return Verified{}, false, errors.New("invalid cached content directory")
	}
	err = filepath.WalkDir(content, func(path string, item os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if item.Type()&os.ModeSymlink != 0 {
			return errors.New("cache links forbidden")
		}
		if item.IsDir() {
			return nil
		}
		name, _ := filepath.Rel(content, path)
		digest, ok := expected[filepath.ToSlash(name)]
		if !ok {
			return errors.New("unexpected cache content")
		}
		actual, err := hashFile(ctx, path)
		if err != nil {
			return err
		}
		if actual != digest {
			return errors.New("cached resource content changed")
		}
		delete(expected, filepath.ToSlash(name))
		return nil
	})
	if err != nil {
		return Verified{}, false, err
	}
	if len(expected) != 0 {
		return Verified{}, false, errors.New("cached resource content missing")
	}
	return Verified{content, r.SHA256}, true, nil
}
