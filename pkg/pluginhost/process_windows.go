package pluginhost

import (
	"os"
	"syscall"
)

var processMissing = os.ErrProcessDone

func processAttributes() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x200}
}

// All product launches additionally use processscope's Job Object. This kill is
// only the immediate process stop; Scope.Finish confirms descendant cleanup.
func killProcess(pid int) error {
	p, e := os.FindProcess(pid)
	if e != nil {
		return e
	}
	defer p.Release()
	return p.Kill()
}
