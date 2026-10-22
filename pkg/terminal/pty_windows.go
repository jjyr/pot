package terminal

import (
	"github.com/jjyr/pot/pkg/processscope"
	"github.com/jjyr/pot/pkg/winpty"
	"os"
	"os/exec"
	"path/filepath"
)

type nativeTerminal struct {
	console *winpty.Console
	scope   *processscope.Scope
}

func startTerminal(cwd string, r Create) (*exec.Cmd, *os.File, int, *nativeTerminal, error) {
	cmd := exec.Command(filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe"), "-NoLogo", "-NoProfile", "-NoExit", "-Command", "Remove-Module PSReadLine -ErrorAction SilentlyContinue")
	cmd.Dir = cwd
	cmd.Env = os.Environ()
	scope, err := processscope.Prepare(cmd, nil, processscope.Markers{})
	if err != nil {
		return nil, nil, 0, nil, err
	}
	c, err := winpty.Start(cmd, r.Cols, r.Rows)
	if err != nil {
		scope.Abort()
		return nil, nil, 0, nil, err
	}
	if err = scope.Started(); err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		scope.Abort()
		c.Close()
		return nil, nil, 0, nil, err
	}
	return cmd, c.Output, 0, &nativeTerminal{c, scope}, nil
}
func (s *Session) finishNative() error {
	err := s.native.scope.Finish()
	s.native.console.Close()
	return err
}
func (s *Session) resize(size Size) error { return s.native.console.Resize(size.Cols, size.Rows) }
func (s *Session) killNative()            { _ = s.native.scope.Stop(s.cmd.Process.Pid) }

func (s *Session) input() *os.File { return s.native.console.Input }
