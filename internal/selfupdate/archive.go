package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Extraction never follows archive links. Links are installed only after all
// regular files, with confined targets; special files and duplicate paths fail.
func extract(archive, destination string, zipped bool) error {
	seen := map[string]bool{}
	links := map[string]string{}
	var total int64
	add := func(name, link string, mode os.FileMode, size int64, read io.Reader) error {
		clean := filepath.Clean(filepath.FromSlash(name))
		if clean == "." {
			return nil
		}
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || seen[clean] || len(seen) > 100000 {
			return errors.New("unsafe archive path")
		}
		seen[clean] = true
		target := filepath.Join(destination, clean)
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		if mode.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		if mode&os.ModeSymlink != 0 {
			resolved := filepath.Clean(filepath.Join(filepath.Dir(clean), filepath.FromSlash(link)))
			if filepath.IsAbs(link) || resolved == ".." || strings.HasPrefix(resolved, ".."+string(filepath.Separator)) {
				return errors.New("unsafe archive link")
			}
			links[target] = link
			return nil
		}
		if !mode.IsRegular() || size < 0 || size > 1<<30 {
			return errors.New("unsupported archive entry")
		}
		total += size
		if total > 2<<30 {
			return errors.New("expanded update size limit")
		}
		permissions := os.FileMode(0644)
		if mode&0111 != 0 {
			permissions = 0755
		}
		file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, permissions)
		if err != nil {
			return err
		}
		n, err := io.Copy(file, io.LimitReader(read, size+1))
		closeErr := file.Close()
		if err == nil && n != size {
			err = errors.New("archive entry size changed")
		}
		return errors.Join(err, closeErr)
	}
	if zipped {
		reader, err := zip.OpenReader(archive)
		if err != nil {
			return err
		}
		defer reader.Close()
		for _, f := range reader.File {
			r, err := f.Open()
			if err != nil {
				return err
			}
			link := ""
			if f.Mode()&os.ModeSymlink != 0 {
				data, e := io.ReadAll(io.LimitReader(r, 4097))
				if e != nil || len(data) > 4096 {
					r.Close()
					return errors.New("invalid archive link")
				}
				link = string(data)
			}
			err = add(f.Name, link, f.Mode(), int64(f.UncompressedSize64), r)
			r.Close()
			if err != nil {
				return err
			}
		}
	} else {
		file, err := os.Open(archive)
		if err != nil {
			return err
		}
		defer file.Close()
		gz, err := gzip.NewReader(file)
		if err != nil {
			return err
		}
		defer gz.Close()
		reader := tar.NewReader(gz)
		for {
			h, err := reader.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeDir && h.Typeflag != tar.TypeSymlink {
				return errors.New("unsupported archive entry type")
			}
			if err = add(h.Name, h.Linkname, h.FileInfo().Mode(), h.Size, reader); err != nil {
				return err
			}
		}
	}
	// A link cannot be an ancestor of another entry or another link.
	for target, link := range links {
		rel, _ := filepath.Rel(destination, target)
		for name := range seen {
			if strings.HasPrefix(name, rel+string(filepath.Separator)) {
				return errors.New("archive link contains entries")
			}
		}
		if err := os.Symlink(link, target); err != nil {
			return err
		}
	}
	return nil
}
