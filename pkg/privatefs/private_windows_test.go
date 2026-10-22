package privatefs

import (
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsPrivateACLAndLocks(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private")
	if err := Mkdir(dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "lock")
	f, err := Lock(path, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if other, err := Lock(path, false, true); err == nil {
		other.Close()
		t.Fatal("duplicate exclusive lock accepted")
	}
	f.Close()
	f, err = Lock(path, false, true)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	// An inherited broad ACL must never qualify merely because chmod succeeds.
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	acl, _, _ := sd.DACL()
	if err = windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
	if err = Check(path); err == nil {
		t.Fatal("world-readable credential accepted")
	}
	if file, err := Open(path, os.O_RDONLY); err == nil {
		file.Close()
		t.Fatal("world-readable file opened")
	}
}

func TestWindowsPrivateTemporaryFile(t *testing.T) {
	f, err := CreateTemp(t.TempDir(), "private-")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err = CheckFile(f); err != nil {
		t.Fatal(err)
	}
}
