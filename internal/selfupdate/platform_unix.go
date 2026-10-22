//go:build darwin || linux

package selfupdate

import (
	"errors"
	"os"
	"syscall"
)

const (
	lockShared    = syscall.LOCK_SH
	lockExclusive = syscall.LOCK_EX
	lockNonblock  = syscall.LOCK_NB
)

var connectionRefused = syscall.ECONNREFUSED

func updatesSupported() bool                { return true }
func restartHost(path string) error         { return syscall.Exec(path, os.Args, os.Environ()) }
func detachedProcess() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }
func privateDirectory(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || info.Mode().Perm()&0077 != 0 || stat.Uid != uint32(os.Geteuid()) {
		return errors.New("update directory must be private and owned")
	}
	return nil
}
func openLock(path string, how int) (*os.File, error) {
	fd, err := syscall.Open(path, syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	if err = syscall.Flock(fd, how); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
