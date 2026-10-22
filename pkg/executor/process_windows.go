package executor

import (
	"errors"
	"github.com/jjyr/pot/pkg/winpty"
	"golang.org/x/sys/windows"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type nativeTerminal struct{ console *winpty.Console }

func processGroup() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
}
func configureProcess(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = processGroup()
	}
}
func killGroup(pid int) error {
	p, e := os.FindProcess(pid)
	if e != nil {
		return e
	}
	defer p.Release()
	return p.Kill()
}
func confirmGroupGone(int, time.Time) error {
	return errors.New("Windows cleanup requires Job Object accounting")
}
func terminalEOF(err error) bool { return errors.Is(err, windows.ERROR_BROKEN_PIPE) }
func (p *process) startTTY(cmd *exec.Cmd) error {
	c, err := winpty.Start(cmd, 80, 24)
	if err != nil {
		return err
	}
	p.native = &nativeTerminal{c}
	p.terminal = c.Output
	p.input = c.Input
	p.readers = []*os.File{c.Output}
	return nil
}
func (p *process) interrupt() error {
	if p.native != nil {
		_, err := p.input.Write([]byte{3})
		return err
	}
	return p.supervisor.Stop(p.cmd.Process.Pid)
}
func (p *process) resize(args map[string]any) error {
	var cols, rows uint16
	if v, ok := args["cols"]; ok {
		n, _ := integer(v)
		cols = uint16(n)
	}
	if v, ok := args["rows"]; ok {
		n, _ := integer(v)
		rows = uint16(n)
	}
	return p.native.console.Resize(cols, rows)
}
func (p *process) closeNative() { p.native.console.Close() }
func supportedShell(path string) bool {
	switch strings.ToLower(filepath.Base(path)) {
	case "powershell.exe", "pwsh.exe":
		return true
	}
	return false
}
func commandArgs(_ string, source string, login bool) []string {
	args := []string{"-NoLogo", "-NonInteractive"}
	if !login {
		args = append(args, "-NoProfile")
	}
	// The tool protocol is UTF-8, including redirected PowerShell 5.1 output.
	source = "[Console]::InputEncoding=[System.Text.UTF8Encoding]::new($false);[Console]::OutputEncoding=[System.Text.UTF8Encoding]::new($false);$OutputEncoding=[Console]::OutputEncoding;" + source
	return append(args, "-Command", source)
}
