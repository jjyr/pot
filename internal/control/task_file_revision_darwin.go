package control

import (
	"errors"
	"github.com/jjyr/pot/pkg/core"
	"os"
	"syscall"
)

func taskFileRevision(_ *os.File, info os.FileInfo) (string, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", errors.New("Task file identity unavailable")
	}
	return core.HashJSON([]any{stat.Dev, stat.Ino, stat.Ctimespec.Sec, stat.Ctimespec.Nsec, stat.Mtimespec.Sec, stat.Mtimespec.Nsec, stat.Size, stat.Mode})
}

func taskDirectoryIdentity(_ string, info os.FileInfo) (string, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() {
		return "", errors.New("Task workspace directory identity unavailable")
	}
	return core.HashJSON([]any{stat.Dev, stat.Ino})
}
