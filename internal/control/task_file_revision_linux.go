package control

import (
	"errors"
	"github.com/jjyr/pot/pkg/core"
	"os"
	"syscall"
)

func taskFileRevision(info os.FileInfo) (string, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", errors.New("Task file identity unavailable")
	}
	return core.HashJSON([]any{stat.Dev, stat.Ino, stat.Ctim.Sec, stat.Ctim.Nsec, stat.Mtim.Sec, stat.Mtim.Nsec, stat.Size, stat.Mode})
}

func taskDirectoryIdentity(info os.FileInfo) (string, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() {
		return "", errors.New("Task workspace directory identity unavailable")
	}
	return core.HashJSON([]any{stat.Dev, stat.Ino})
}
