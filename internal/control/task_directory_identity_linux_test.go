package control

import (
	"testing"

	"golang.org/x/sys/unix"
)

func TestTaskDirectoryIdentityRequiresCreationEvidence(t *testing.T) {
	stat := unix.Statx_t{Mask: unix.STATX_TYPE | unix.STATX_INO, Mode: unix.S_IFDIR, Ino: 42}
	if _, err := taskDirectoryStatxIdentity(stat); err == nil {
		t.Fatal("accepted directory without OS creation evidence")
	}
	stat.Mask |= unix.STATX_BTIME
	stat.Btime = unix.StatxTimestamp{Sec: 100, Nsec: 200}
	first, err := taskDirectoryStatxIdentity(stat)
	if err != nil {
		t.Fatal(err)
	}
	stat.Btime.Nsec++
	second, err := taskDirectoryStatxIdentity(stat)
	if err != nil || first == second {
		t.Fatal("reused device/inode hid a new directory instance", first, second, err)
	}
}
