//go:build linux

package executor

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

// Pin parent and inode before writing through an exact-file grant.
// The advisory lock only excludes cooperating writers.
func writePatchInPlace(path string, data []byte, original *patchOriginal) (result error) {
	parent, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer parent.Close()
	info, err := parent.Stat(".")
	if err != nil {
		return err
	}
	if !os.SameFile(info, original.parent) {
		return errors.New("patch parent changed since preflight")
	}
	file, err := parent.OpenFile(filepath.Base(path), os.O_RDWR|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, file.Close()) }()
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return fmt.Errorf("patch file is busy: %w", err)
	}
	// Closing file releases the lock, including on all error paths.
	info, err = file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || !os.SameFile(info, original.file) || info.Mode() != original.file.Mode() {
		return errors.New("patch file changed since preflight")
	}
	// File.Stat already fetched the inode metadata, including its link count.
	stat := info.Sys().(*syscall.Stat_t)
	if stat.Nlink != 1 {
		return errors.New("cannot patch a multiply linked file in place")
	}
	current, err := io.ReadAll(io.LimitReader(file, patchLimit+1))
	if err != nil {
		return err
	}
	if !bytes.Equal(current, original.data) {
		return errors.New("patch contents changed since preflight")
	}
	return replacePatchContents(file, data, original.data)
}

type patchContentFile interface {
	WriteAt([]byte, int64) (int, error)
	Truncate(int64) error
	Sync() error
}

func replacePatchContents(file patchContentFile, data, original []byte) error {
	write := func(content []byte) error {
		n, err := file.WriteAt(content, 0)
		if err != nil {
			return err
		}
		if n != len(content) {
			return io.ErrShortWrite
		}
		if err := file.Truncate(int64(len(content))); err != nil {
			return err
		}
		return file.Sync()
	}
	if err := write(data); err != nil {
		if restore := write(original); restore != nil {
			return errors.Join(err, fmt.Errorf("patch restoration failed; file may be partially modified: %w", restore))
		}
		return fmt.Errorf("patch write failed; original contents restored: %w", err)
	}
	return nil
}
