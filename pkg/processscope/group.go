package processscope

import (
	"errors"
	"fmt"
	"syscall"
	"time"
)

// KillGroup repeatedly stops a process group until its disappearance is confirmed.
// Darwin returns EPERM for groups containing only zombies; that is neither a
// permanent signaling failure nor proof of cleanup. Wait for reaping within the
// deadline, and report persistent permission errors instead of accepting them.
func KillGroup(pid int, deadline time.Time) error {
	return killGroup(pid, deadline, syscall.Kill)
}

func killGroup(pid int, deadline time.Time, signal func(int, syscall.Signal) error) error {
	if pid <= 0 {
		return fmt.Errorf("invalid process group %d", pid)
	}
	for {
		err := signal(-pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		if err != nil && !errors.Is(err, syscall.EPERM) {
			return err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return errors.Join(fmt.Errorf("process group %d cleanup deadline exceeded: %w", pid, syscall.ETIMEDOUT), err)
		}
		time.Sleep(min(10*time.Millisecond, remaining))
	}
}
