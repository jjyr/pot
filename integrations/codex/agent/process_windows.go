package agent

import (
	"golang.org/x/sys/windows"
	"syscall"
)

func daemonAttributes() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP}
}
func childAttributes() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
}
func openExternalURL(address string) error {
	verb, err := windows.UTF16PtrFromString("open")
	if err != nil {
		return err
	}
	target, err := windows.UTF16PtrFromString(address)
	if err != nil {
		return err
	}
	return windows.ShellExecute(0, verb, target, nil, nil, windows.SW_SHOWNORMAL)
}
