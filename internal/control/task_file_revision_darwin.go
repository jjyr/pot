package control

import (
	"errors"
	"github.com/jjyr/pot/internal/core"
	"os"
	"syscall"
)

func taskFileRevision(info os.FileInfo) (string, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", errors.New("Task file identity unavailable")
	}
	return core.HashJSON([]any{stat.Dev, stat.Ino, stat.Ctimespec.Sec, stat.Ctimespec.Nsec, stat.Mtimespec.Sec, stat.Mtimespec.Nsec, stat.Size, stat.Mode})
}
