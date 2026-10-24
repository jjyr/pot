package selfupdate

import (
	"context"
	"errors"
	"golang.org/x/sys/unix"
)

type processWatch struct{ fd int }

func watchProcess(pid int) (*processWatch, error) {
	fd, err := unix.PidfdOpen(pid, 0)
	if err != nil {
		return nil, err
	}
	return &processWatch{fd}, nil
}
func (w *processWatch) Close() error { return unix.Close(w.fd) }
func (w *processWatch) Wait(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		fds := []unix.PollFd{{Fd: int32(w.fd), Events: unix.POLLIN}}
		n, err := unix.Poll(fds, 100)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return err
		}
		if n > 0 {
			return nil
		}
	}
}
