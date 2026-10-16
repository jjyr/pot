package resource

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/instance"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// archiveFiles derives authority from the verified archive on every cache read.
// An editable completion marker cannot authorize altered extracted content.
func archiveFiles(ctx context.Context, blob string, r Resource, destination string) (map[string]string, error) {
	files := map[string]string{}
	if r.Format == Raw {
		files["payload"] = r.SHA256
		if destination != "" {
			if err := copyFile(ctx, blob, filepath.Join(destination, "payload")); err != nil {
				return nil, err
			}
		}
		return files, nil
	}
	input, err := os.Open(blob)
	if err != nil {
		return nil, err
	}
	defer input.Close()
	compressed := bufio.NewReader(&contextReader{ctx, input})
	gz, err := gzip.NewReader(compressed)
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	gz.Multistream(false)
	archive := tar.NewReader(gz)
	total := uint64(0)
	entries := 0
	for {
		header, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		entries++
		if entries > 100000 {
			return nil, errors.New("resource archive entry limit")
		}
		name := header.Name
		if header.Typeflag == tar.TypeDir {
			name = strings.TrimSuffix(name, "/")
		}
		if !core.SafeRelativePath(name) {
			return nil, errors.New("unsafe resource archive path")
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA && header.Typeflag != tar.TypeDir {
			return nil, errors.New("resource links and special files forbidden")
		}
		path := filepath.Join(destination, filepath.FromSlash(name))
		if header.Typeflag == tar.TypeDir {
			if destination != "" {
				if err = os.MkdirAll(path, 0700); err != nil {
					return nil, err
				}
			}
			continue
		}
		if _, duplicate := files[name]; duplicate {
			return nil, errors.New("duplicate resource archive file")
		}
		if header.Size < 0 || uint64(header.Size) > r.UnpackedBytes-total {
			return nil, errors.New("resource archive exceeds unpacked size")
		}
		total += uint64(header.Size)
		h := sha256.New()
		var writer io.Writer = h
		var file *os.File
		if destination != "" {
			if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				return nil, err
			}
			file, err = os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err != nil {
				return nil, err
			}
			writer = io.MultiWriter(h, file)
		}
		count, copyErr := io.Copy(writer, &contextReader{ctx, archive})
		if file != nil {
			if copyErr == nil {
				copyErr = file.Sync()
			}
			closeErr := file.Close()
			if copyErr == nil {
				copyErr = closeErr
			}
		}
		if copyErr != nil {
			return nil, copyErr
		}
		if count != header.Size {
			return nil, errors.New("truncated resource archive file")
		}
		files[name] = hex.EncodeToString(h.Sum(nil))
	}
	if total != r.UnpackedBytes || files["index.json"] == "" || files["oci-layout"] == "" {
		return nil, errors.New("invalid OCI resource layout")
	}
	// Consume bounded tar padding to validate gzip's trailer and reject a hidden
	// second payload. Standard OCI exporters finish with zero-filled padding.
	padding, err := io.ReadAll(io.LimitReader(gz, 1024*1024+1))
	if err != nil {
		return nil, err
	}
	if len(padding) > 1024*1024 {
		return nil, errors.New("excessive archive padding")
	}
	for _, b := range padding {
		if b != 0 {
			return nil, errors.New("unexpected data after OCI archive")
		}
	}
	if _, err := compressed.Peek(1); err != io.EOF {
		return nil, errors.New("unexpected compressed data after OCI archive")
	}
	return files, nil
}
func copyFile(ctx context.Context, source, target string) error {
	return copyFileBounded(ctx, source, target, 0)
}
func copyFileBounded(ctx context.Context, source, target string, limit uint64) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	var reader io.Reader = in
	if limit > 0 {
		reader = io.LimitReader(reader, int64(limit)+1)
	}
	var count int64
	count, err = io.Copy(out, &contextReader{ctx, reader})
	if err == nil && limit > 0 && uint64(count) != limit {
		err = errors.New("resource copy size mismatch")
	}
	if err == nil {
		err = out.Sync()
	}
	closeErr := out.Close()
	if err != nil {
		return err
	}
	return closeErr
}
func unpack(ctx context.Context, dir string, r Resource) error {
	content := filepath.Join(dir, "content")
	if err := os.Mkdir(content, 0700); err != nil {
		return err
	}
	files, err := archiveFiles(ctx, filepath.Join(dir, "blob"), r, content)
	if err != nil {
		return err
	}
	return instance.WriteJSON(filepath.Join(dir, "complete.json"), files)
}
