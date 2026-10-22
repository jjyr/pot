//go:build darwin || linux

package terminal

import (
	"github.com/creack/pty"
	"golang.org/x/sys/unix"
	"os"
	"os/exec"
	"syscall"
)

type nativeTerminal struct{}

func startTerminal(cwd string, r Create) (*exec.Cmd, *os.File, int, *nativeTerminal, error) {
	cmd := exec.Command("/bin/bash", "--noprofile", "--norc", "-i")
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "HISTFILE=/dev/null", "TERM=xterm-256color")
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: r.Cols, Rows: r.Rows})
	if err != nil {
		return nil, nil, 0, nil, err
	}
	fd, err := unix.Dup(int(f.Fd()))
	if err == nil {
		unix.CloseOnExec(fd)
		err = unix.SetNonblock(fd, true)
	}
	if err != nil {
		if fd >= 0 {
			unix.Close(fd)
		}
		f.Close()
		killSession(cmd.Process.Pid)
		cmd.Wait()
		return nil, nil, 0, nil, err
	}
	master := os.NewFile(uintptr(fd), f.Name())
	f.Close()
	f = master
	return cmd, f, fd, nil, nil
}
func (s *Session) finishNative() error { return nil }
func (s *Session) resize(size Size) error {
	return unix.IoctlSetWinsize(s.fd, unix.TIOCSWINSZ, &unix.Winsize{Col: size.Cols, Row: size.Rows})
}
func (s *Session) killNative() {
	if group, err := unix.IoctlGetInt(s.fd, unix.TIOCGPGRP); err == nil && group > 0 {
		syscall.Kill(-group, syscall.SIGKILL)
	}
	killSession(s.cmd.Process.Pid)
}

func (s *Session) input() *os.File { return s.master }
