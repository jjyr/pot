package resource

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// DiskUsage reports stored regular-file bytes without following cache symlinks.
// This remains available for corrupt entries which the owner may need to prune.
func (s *Store) DiskUsage(ctx context.Context, r Resource) (bool, uint64, error) {
	if err := r.Validate(); err != nil {
		return false, 0, err
	}
	root := filepath.Join(s.root, r.SHA256)
	info, err := os.Lstat(root)
	if os.IsNotExist(err) {
		return false, 0, nil
	}
	if err != nil {
		return false, 0, err
	}
	if !info.IsDir() {
		return true, uint64(max(0, info.Size())), nil
	}
	var size uint64
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		size += uint64(max(0, info.Size()))
		return nil
	})
	return true, size, err
}

// Prune excludes imports/transfers for this digest. The caller owns product
// references; Store owns the filesystem boundary and concurrent preparations.
func (s *Store) Prune(ctx context.Context, r Resource) error {
	if err := r.Validate(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errors.New("resource store closed")
	}
	if j := s.jobs[r.SHA256]; j != nil && !j.done {
		s.mu.Unlock()
		return errors.New("resource is being prepared")
	}
	lock := s.lockLocked(r.SHA256)
	select {
	case <-lock:
	default:
		s.mu.Unlock()
		return errors.New("resource is being prepared")
	}
	s.workers.Add(1)
	s.mu.Unlock()
	defer s.workers.Done()
	defer func() { lock <- struct{}{} }()
	root, err := os.OpenRoot(s.root)
	if err != nil {
		return err
	}
	defer root.Close()
	if err = root.RemoveAll(r.SHA256); err != nil {
		return err
	}
	directory, err := os.Open(s.root)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
