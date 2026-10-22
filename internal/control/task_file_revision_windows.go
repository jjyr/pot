package control

import (
	"errors"
	"github.com/jjyr/pot/pkg/core"
	"golang.org/x/sys/windows"
	"os"
	"unsafe"
)

func taskFileRevision(file *os.File, info os.FileInfo) (string, error) {
	var id windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &id); err != nil {
		return "", err
	}
	var basic fileBasicInfo
	if err := windows.GetFileInformationByHandleEx(windows.Handle(file.Fd()), windows.FileBasicInfo, (*byte)(unsafe.Pointer(&basic)), uint32(unsafe.Sizeof(basic))); err != nil {
		return "", err
	}
	return core.HashJSON([]any{id.VolumeSerialNumber, id.FileIndexHigh, id.FileIndexLow, basic.LastWriteTime, basic.ChangeTime, info.Size(), info.Mode()})
}
func taskDirectoryIdentity(path string, original os.FileInfo) (string, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	h, err := windows.CreateFile(p, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", err
	}
	f := os.NewFile(uintptr(h), path)
	defer f.Close()
	// os.SameFile compares Windows volume serial and file index.
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !os.SameFile(original, info) {
		return "", errors.New("Task workspace directory identity changed")
	}
	var id windows.ByHandleFileInformation
	if err = windows.GetFileInformationByHandle(h, &id); err != nil {
		return "", err
	}
	return core.HashJSON([]any{id.VolumeSerialNumber, id.FileIndexHigh, id.FileIndexLow})
}

type fileBasicInfo struct {
	CreationTime, LastAccessTime, LastWriteTime, ChangeTime int64
	FileAttributes                                          uint32
}
