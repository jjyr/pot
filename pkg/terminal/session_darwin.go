package terminal

import "syscall"

func killSession(pid int) { syscall.Kill(-pid, syscall.SIGKILL) }
