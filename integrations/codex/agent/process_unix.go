//go:build darwin || linux

package agent

import (
	"os/exec"
	"runtime"
	"syscall"
)

func daemonAttributes() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }
func childAttributes() *syscall.SysProcAttr  { return &syscall.SysProcAttr{Setpgid: true} }
func openExternalURL(address string) error {
	binary := "xdg-open"
	if runtime.GOOS == "darwin" {
		binary = "open"
	}
	return exec.Command(binary, address).Run()
}
