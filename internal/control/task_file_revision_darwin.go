package control

import (
	"errors"
	"os"
	"syscall"

	"github.com/jjyr/pot/pkg/core"
)

func taskFileRevision(info os.FileInfo) (string, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", errors.New("Task file identity unavailable")
	}
	return core.HashJSON([]any{stat.Dev, stat.Ino, stat.Ctimespec.Sec, stat.Ctimespec.Nsec, stat.Mtimespec.Sec, stat.Mtimespec.Nsec, stat.Size, stat.Mode})
}

func taskDirectoryIdentity(root *os.Root) (string, error) {
	info, err := root.Stat(".")
	if err != nil {
		return "", err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || (stat.Birthtimespec.Sec == 0 && stat.Birthtimespec.Nsec == 0) {
		return "", errors.New("Task workspace directory identity unavailable")
	}
	id, err := core.HashJSON([]any{"darwin", stat.Dev, stat.Ino, stat.Birthtimespec.Sec, stat.Birthtimespec.Nsec})
	return "directory-v2:" + id, err
}
