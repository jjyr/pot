package secrets

import (
	"context"
	"crypto/rand"
	"errors"
	"golang.org/x/sys/windows"
	"unsafe"
)

// Generic credentials are encrypted by Windows and scoped to the logged-on user.
// The raw master key never enters a filesystem or a command line.
type credential struct {
	Flags, Type             uint32
	TargetName, Comment     *uint16
	LastWritten             windows.Filetime
	BlobSize                uint32
	Blob                    *byte
	Persist, AttributeCount uint32
	Attributes              uintptr
	Alias, UserName         *uint16
}

var credDLL = windows.NewLazySystemDLL("advapi32.dll")

func platformKey(ctx context.Context, account string, allowCreate bool) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	target, err := windows.UTF16PtrFromString("pot.control.secrets/" + account)
	if err != nil {
		return nil, err
	}
	read := func() ([]byte, error) {
		var p *credential
		r, _, e := credDLL.NewProc("CredReadW").Call(uintptr(unsafe.Pointer(target)), 1, 0, uintptr(unsafe.Pointer(&p)))
		if r == 0 {
			return nil, e
		}
		defer credDLL.NewProc("CredFree").Call(uintptr(unsafe.Pointer(p)))
		if p.BlobSize != 32 {
			return nil, errors.New("invalid Windows master credential")
		}
		return append([]byte{}, unsafe.Slice(p.Blob, 32)...), nil
	}
	// Serialize first creation across processes without exposing key material.
	mutexName, err := windows.UTF16PtrFromString(`Local\pot-secrets-` + account)
	if err != nil {
		return nil, err
	}
	mutex, err := windows.CreateMutex(nil, false, mutexName)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(mutex)
	wait, err := windows.WaitForSingleObject(mutex, 30000)
	if err != nil {
		return nil, err
	}
	if wait != windows.WAIT_OBJECT_0 && wait != windows.WAIT_ABANDONED {
		return nil, errors.New("Windows master credential lock timed out")
	}
	defer windows.ReleaseMutex(mutex)
	key, err := read()
	if err == nil {
		return key, nil
	}
	if !errors.Is(err, windows.ERROR_NOT_FOUND) {
		return nil, err
	}
	if !allowCreate {
		return nil, errors.New("secret master key is missing; restore the original credential")
	}
	key = make([]byte, 32)
	if _, err = rand.Read(key); err != nil {
		return nil, err
	}
	defer clear(key)
	c := credential{Type: 1, TargetName: target, BlobSize: 32, Blob: &key[0], Persist: 2}
	r, _, e := credDLL.NewProc("CredWriteW").Call(uintptr(unsafe.Pointer(&c)), 0)
	if r == 0 {
		return nil, e
	}
	return read()
}
