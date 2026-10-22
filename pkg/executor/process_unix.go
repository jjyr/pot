//go:build darwin || linux

package executor

import (
	"errors"
	"fmt"
	"github.com/creack/pty"
	"golang.org/x/sys/unix"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

type nativeTerminal struct{}

func killGroup(pid int) error {
	err := syscall.Kill(-pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}
func confirmGroupGone(pid int, deadline time.Time) error {
	for time.Now().Before(deadline) {
		err := syscall.Kill(-pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		if err != nil {
			return errors.Join(ErrCleanupUnconfirmed, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("%w: process group %d remains", ErrCleanupUnconfirmed, pid)
}

func terminalEOF(err error) bool     { return errors.Is(err, syscall.EIO) }
func configureProcess(cmd *exec.Cmd) { cmd.SysProcAttr = processGroup() }
func (p *process) startTTY(command *exec.Cmd) error {
	terminal, err := pty.StartWithSize(command, &pty.Winsize{Rows: 24, Cols: 80})
	if err != nil {
		return err
	}
	// creack/pty's master starts in blocking mode. Rewrap a nonblocking
	// duplicate so Go's poller can interrupt blocked reads/writes on cleanup.
	fd, err := unix.Dup(int(terminal.Fd()))
	if err == nil {
		unix.CloseOnExec(fd)
		err = unix.SetNonblock(fd, true)
	}
	if err != nil {
		if fd >= 0 {
			unix.Close(fd)
		}
		terminal.Close()
		killGroup(command.Process.Pid)
		command.Wait()
		return err
	}
	master := os.NewFile(uintptr(fd), terminal.Name())
	terminal.Close()
	p.terminal = master
	p.input = master
	p.readers = []*os.File{master}
	return nil
}
func (p *process) interrupt() error {
	group := p.cmd.Process.Pid
	if p.terminal != nil {
		var foreground int
		err := p.terminalControl(func(fd int) error { var err error; foreground, err = unix.IoctlGetInt(fd, unix.TIOCGPGRP); return err })
		if err != nil {
			return err
		}
		group = foreground
	}
	if err := syscall.Kill(-group, syscall.SIGINT); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}
func (p *process) resize(args map[string]any) error {
	err := p.terminalControl(func(fd int) error {
		size, err := unix.IoctlGetWinsize(fd, unix.TIOCGWINSZ)
		if err != nil {
			return err
		}
		if value, ok := args["rows"]; ok {
			n, _ := integer(value)
			size.Row = uint16(n)
		}
		if value, ok := args["cols"]; ok {
			n, _ := integer(value)
			size.Col = uint16(n)
		}
		return unix.IoctlSetWinsize(fd, unix.TIOCSWINSZ, size)
	})
	if err != nil {
		return err
	}
	return nil
}
func (p *process) closeNative() {}
func supportedShell(path string) bool {
	switch filepath.Base(path) {
	case "sh", "bash", "zsh", "dash":
		return true
	}
	return false
}
func commandArgs(_ string, source string, login bool) []string {
	flag := "-c"
	if login {
		flag = "-lc"
	}
	return []string{flag, source}
}
