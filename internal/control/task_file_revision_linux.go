package control

import (
	"errors"
	"os"
	"syscall"

	"github.com/jjyr/pot/pkg/core"
	"golang.org/x/sys/unix"
)

func taskFileRevision(info os.FileInfo) (string, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", errors.New("Task file identity unavailable")
	}
	return core.HashJSON([]any{stat.Dev, stat.Ino, stat.Ctim.Sec, stat.Ctim.Nsec, stat.Mtim.Sec, stat.Mtim.Nsec, stat.Size, stat.Mode})
}

func taskDirectoryIdentity(root *os.Root) (string, error) {
	// Read immutable creation evidence from the same directory instance as the
	// opened download root. Path stat and mutable ctime/mtime cannot provide it.
	file, err := root.Open(".")
	if err != nil {
		return "", err
	}
	defer file.Close()
	var stat unix.Statx_t
	if err := unix.Statx(int(file.Fd()), "", unix.AT_EMPTY_PATH, unix.STATX_TYPE|unix.STATX_INO|unix.STATX_BTIME, &stat); err != nil {
		return "", err
	}
	return taskDirectoryStatxIdentity(stat)
}

func taskDirectoryStatxIdentity(stat unix.Statx_t) (string, error) {
	const required = unix.STATX_TYPE | unix.STATX_INO | unix.STATX_BTIME
	if stat.Mask&required != required || stat.Mode&unix.S_IFMT != unix.S_IFDIR {
		return "", errors.New("Task workspace directory identity unavailable")
	}
	id, err := core.HashJSON([]any{"linux", stat.Dev_major, stat.Dev_minor, stat.Ino, stat.Btime.Sec, stat.Btime.Nsec})
	return "directory-v2:" + id, err
}
