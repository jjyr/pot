package composition

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

func configureWorker(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.Env = []string{"SystemRoot=" + os.Getenv("SystemRoot"), "PATH=" + filepath.Join(os.Getenv("SystemRoot"), "System32")}
	cmd.Cancel = func() error { return cmd.Process.Kill() }
}
