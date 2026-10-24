//go:build darwin && cgo

package processscope

import (
	"errors"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// Keep parents alive briefly so they can reap children before they too are
// stopped. This grace is shared by the entire tree, regardless of its depth.
const darwinReapGrace = 250 * time.Millisecond

func cancelOriginal(observer *nativeObserver, original *os.Process, done <-chan error) error {
	deadline := time.NewTimer(darwinReapGrace)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	firstScan := true
	for {
		select {
		case <-done:
			return nil
		case <-deadline.C:
			return stopOriginal(original, done)
		default:
		}
		observer.mu.Lock()
		members, err := observer.members()
		err = errors.Join(err, observer.err)
		observer.mu.Unlock()
		if err != nil {
			return errors.Join(err, stopOriginal(original, done))
		}
		parents := make(map[uint64]bool, len(members))
		hasDescendants := false
		for _, member := range members {
			parents[member.parent] = true
			hasDescendants = hasDescendants || member.id != observer.root
		}
		// Only skip grace for an initially childless workload. Once descendants
		// have exited, their parent may still be handling the exit notification.
		if firstScan && !hasDescendants {
			return stopOriginal(original, done)
		}
		firstScan = false
		for _, member := range members {
			select {
			case <-deadline.C:
				return stopOriginal(original, done)
			default:
			}
			if member.id == observer.root || parents[member.id] {
				continue
			}
			// A snapshot is not authority to signal a subsequently reused PID.
			id, _, ok := identity(member.pid)
			if !ok || id != member.id {
				continue
			}
			if err := unix.Kill(member.pid, unix.SIGKILL); err != nil && !errors.Is(err, unix.ESRCH) {
				return errors.Join(err, stopOriginal(original, done))
			}
		}
		select {
		case <-done:
			return nil
		case <-deadline.C:
			return stopOriginal(original, done)
		case <-tick.C:
		}
	}
}
