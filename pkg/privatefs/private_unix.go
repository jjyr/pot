//go:build darwin || linux

// Package privatefs implements current-user file ownership and locking.
package privatefs

import (
	"errors"
	"os"
	"syscall"
)

func Check(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
		return errors.New("private current-user path required")
	}
	return nil
}
func CheckFile(f *os.File) error {
	info, err := f.Stat()
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || stat.Uid != uint32(os.Geteuid()) || info.Mode().Perm()&0077 != 0 {
		return errors.New("private current-user file required")
	}
	return nil
}
func Mkdir(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	return Check(path)
}
func Open(path string, flags int) (*os.File, error) {
	return os.OpenFile(path, flags|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
}
func Lock(path string, shared, nonblock bool) (*os.File, error) {
	f, err := Open(path, os.O_CREATE|os.O_RDWR)
	if err != nil {
		return nil, err
	}
	how := syscall.LOCK_EX
	if shared {
		how = syscall.LOCK_SH
	}
	if nonblock {
		how |= syscall.LOCK_NB
	}
	if err = CheckFile(f); err == nil {
		err = syscall.Flock(int(f.Fd()), how)
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
func SyncDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
func Replace(old, new string) error { return os.Rename(old, new) }

// LockFile and UnlockFile support daemon startup handoffs on an owned handle.
func LockFile(f *os.File, nonblock bool) error {
	how := syscall.LOCK_EX
	if nonblock {
		how |= syscall.LOCK_NB
	}
	return syscall.Flock(int(f.Fd()), how)
}
func UnlockFile(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }
func LockBusy(err error) bool {
	return errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN)
}
