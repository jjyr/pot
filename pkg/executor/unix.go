//go:build darwin || linux

package executor

import "syscall"

func processGroup() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setpgid: true} }
