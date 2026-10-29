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
	return cancelDarwinTree(observer.root, func() ([]nativeMember, error) {
		observer.mu.Lock()
		defer observer.mu.Unlock()
		members, err := observer.members()
		return members, errors.Join(err, observer.err)
	}, func() error { return stopOriginal(original, done) }, func(member nativeMember) error {
		// A snapshot is not authority to signal a subsequently reused PID.
		id, _, ok := identity(member.pid)
		if !ok || id != member.id {
			return nil
		}
		err := unix.Kill(member.pid, unix.SIGKILL)
		if errors.Is(err, unix.ESRCH) {
			return nil
		}
		return err
	}, done, deadline.C, tick.C)
}

// The scan and signals remain native; explicit timer channels let the grace
// decisions be tested independently of runner scheduling and process-scan cost.
func cancelDarwinTree(root uint64, scan func() ([]nativeMember, error), stopRoot func() error,
	stopMember func(nativeMember) error, done <-chan error, deadline, tick <-chan time.Time) error {
	firstScan := true
	// A parent becoming childless does not mean its exit handler has completed.
	// Retain parent identities across scans for the entire shared grace window.
	parents := make(map[uint64]bool)
	for {
		select {
		case <-done:
			return nil
		case <-deadline:
			return stopRoot()
		default:
		}
		members, err := scan()
		if err != nil {
			return errors.Join(err, stopRoot())
		}
		hasDescendants := false
		for _, member := range members {
			parents[member.parent] = true
			hasDescendants = hasDescendants || member.id != root
		}
		// Only skip grace for an initially childless workload. Once descendants
		// have exited, their parent may still be handling the exit notification.
		if firstScan && !hasDescendants {
			return stopRoot()
		}
		firstScan = false
		for _, member := range members {
			select {
			case <-deadline:
				return stopRoot()
			default:
			}
			if member.id == root || parents[member.id] {
				continue
			}
			if err := stopMember(member); err != nil {
				return errors.Join(err, stopRoot())
			}
		}
		select {
		case <-done:
			return nil
		case <-deadline:
			return stopRoot()
		case <-tick:
		}
	}
}
