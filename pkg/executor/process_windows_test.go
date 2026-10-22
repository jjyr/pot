package executor

import (
	"golang.org/x/sys/windows"
	"syscall"
)

func testProcessSignal(pid int) error {
	h, e := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if e != nil {
		return syscall.ESRCH
	}
	defer windows.CloseHandle(h)
	var code uint32
	if e = windows.GetExitCodeProcess(h, &code); e != nil {
		return e
	}
	if code != 259 {
		return syscall.ESRCH
	}
	return nil
}
