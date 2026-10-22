//go:build darwin || linux

package pluginhost

import "syscall"

func testProcessSignal(pid int) error { return syscall.Kill(pid, 0) }
