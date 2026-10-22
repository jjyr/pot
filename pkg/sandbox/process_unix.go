//go:build darwin || linux

package sandbox

import (
	"syscall"
)

var processMissing = syscall.ESRCH

func processAttributes() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setpgid: true} }
func killProcess(pid int) error               { return syscall.Kill(-pid, syscall.SIGKILL) }
