package selfupdate

import (
	"context"
	"errors"
	"golang.org/x/sys/unix"
)

type processWatch struct{ fd int }

// Arm NOTE_EXIT before asking Cocoa to terminate, so fast exits and PID reuse
// cannot be mistaken for either successful cleanup or an unknown new process.
func watchProcess(pid int) (*processWatch, error) {
	fd, err := unix.Kqueue()
	if err != nil {
		return nil, err
	}
	unix.CloseOnExec(fd)
	events := []unix.Kevent_t{{Ident: uint64(pid), Filter: unix.EVFILT_PROC, Flags: unix.EV_ADD | unix.EV_ENABLE | unix.EV_ONESHOT, Fflags: unix.NOTE_EXIT}}
	if _, err = unix.Kevent(fd, events, nil, nil); err != nil {
		unix.Close(fd)
		return nil, err
	}
	return &processWatch{fd}, nil
}
func (w *processWatch) Close() error { return unix.Close(w.fd) }
func (w *processWatch) Wait(ctx context.Context) error {
	events := make([]unix.Kevent_t, 1)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		timeout := unix.Timespec{Nsec: 100000000}
		n, err := unix.Kevent(w.fd, nil, events, &timeout)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return err
		}
		if n > 0 {
			if events[0].Flags&unix.EV_ERROR != 0 {
				return unix.Errno(events[0].Data)
			}
			return nil
		}
	}
}
