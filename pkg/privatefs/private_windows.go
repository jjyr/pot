package privatefs

import (
	"errors"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"unsafe"
)

func UserSID() (string, error) {
	t, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return "", err
	}
	defer t.Close()
	u, err := t.GetTokenUser()
	if err != nil {
		return "", err
	}
	return u.User.Sid.String(), nil
}
func SecurityDescriptor() (string, error) {
	sid, err := UserSID()
	return "O:" + sid + "D:P(A;OICI;FA;;;" + sid + ")(A;OICI;FA;;;SY)", err
}
func Check(path string) error {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	h, err := windows.CreateFile(p, windows.READ_CONTROL|windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	return checkHandle(h)
}
func CheckFile(f *os.File) error {
	i, e := f.Stat()
	if e != nil {
		return e
	}
	if !i.Mode().IsRegular() {
		return errors.New("private file must be regular")
	}
	return checkHandle(windows.Handle(f.Fd()))
}
func checkHandle(h windows.Handle) error {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		return err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return errors.New("private path cannot be a reparse point")
	}
	sd, err := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return err
	}
	sid, err := UserSID()
	if err != nil {
		return err
	}
	if owner.String() != sid {
		return errors.New("private path must belong to current user")
	}
	acl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	if acl == nil {
		return errors.New("private path requires a DACL")
	}
	for i := uint16(0); i < acl.AceCount; i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err = windows.GetAce(acl, uint32(i), &ace); err != nil {
			return err
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return errors.New("unsupported private path ACL")
		}
		s := (*windows.SID)(unsafe.Pointer(&ace.SidStart)).String()
		if s != sid && s != "S-1-5-18" {
			return errors.New("private path grants access to another principal")
		}
	}
	return nil
}
func Mkdir(path string) error {
	if _, err := os.Lstat(path); err == nil {
		return Check(path)
	} else if !os.IsNotExist(err) {
		return err
	}
	// Create each missing directory with an explicit protected DACL at creation.
	parent := filepath.Dir(path)
	if parent != path {
		if _, err := os.Stat(parent); os.IsNotExist(err) {
			if err = Mkdir(parent); err != nil {
				return err
			}
		}
	}
	s, err := SecurityDescriptor()
	if err != nil {
		return err
	}
	sd, err := windows.SecurityDescriptorFromString(s)
	if err != nil {
		return err
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	err = windows.CreateDirectory(p, &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd})
	if err != nil && err != windows.ERROR_ALREADY_EXISTS {
		return err
	}
	return Check(path)
}
func Open(path string, flags int) (*os.File, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	access := uint32(windows.GENERIC_READ)
	if flags&os.O_RDWR != 0 {
		access |= windows.GENERIC_WRITE
	}
	disposition := uint32(windows.OPEN_EXISTING)
	if flags&os.O_CREATE != 0 {
		disposition = windows.OPEN_ALWAYS
		if flags&os.O_EXCL != 0 {
			disposition = windows.CREATE_NEW
		}
	}
	s, err := SecurityDescriptor()
	if err != nil {
		return nil, err
	}
	sd, err := windows.SecurityDescriptorFromString(s)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(p, access, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}, disposition, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(h), path)
	if err = CheckFile(f); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
func Lock(path string, shared, nonblock bool) (*os.File, error) {
	f, err := Open(path, os.O_CREATE|os.O_RDWR)
	if err != nil {
		return nil, err
	}
	var flags uint32
	if !shared {
		flags |= windows.LOCKFILE_EXCLUSIVE_LOCK
	}
	if nonblock {
		flags |= windows.LOCKFILE_FAIL_IMMEDIATELY
	}
	err = windows.LockFileEx(windows.Handle(f.Fd()), flags, 0, 1, 0, &windows.Overlapped{})
	if err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// Windows has no directory fsync equivalent. Callers sync file contents before
// publishing with MoveFileEx's write-through rename.
func SyncDir(string) error { return nil }
func Replace(old, new string) error {
	a, err := windows.UTF16PtrFromString(old)
	if err != nil {
		return err
	}
	b, err := windows.UTF16PtrFromString(new)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(a, b, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}

func LockFile(f *os.File, nonblock bool) error {
	flags := uint32(windows.LOCKFILE_EXCLUSIVE_LOCK)
	if nonblock {
		flags |= windows.LOCKFILE_FAIL_IMMEDIATELY
	}
	return windows.LockFileEx(windows.Handle(f.Fd()), flags, 0, 1, 0, &windows.Overlapped{})
}
func UnlockFile(f *os.File) error {
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &windows.Overlapped{})
}
func LockBusy(err error) bool { return errors.Is(err, windows.ERROR_LOCK_VIOLATION) }
