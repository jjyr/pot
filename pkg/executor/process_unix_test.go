//go:build darwin || linux

package executor

import "syscall"

func testProcessSignal(pid int) error { return syscall.Kill(pid, 0) }
