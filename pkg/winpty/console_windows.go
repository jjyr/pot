// Package winpty starts Windows 10 1809+ ConPTY processes. Callers must prepare
// a processscope before Start, then call Started before allowing any workload.
package winpty

import (
	"errors"
	"golang.org/x/sys/windows"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"unicode/utf16"
	"unsafe"
)

type Console struct {
	Input, Output *os.File
	handle        windows.Handle
	mu            sync.Mutex
	cols, rows    uint16
}

func Start(cmd *exec.Cmd, cols, rows uint16) (c *Console, err error) {
	if cmd.SysProcAttr == nil || cmd.SysProcAttr.CreationFlags&windows.CREATE_SUSPENDED == 0 {
		return nil, errors.New("ConPTY requires a prepared process scope")
	}
	inRead, inWrite, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	defer inRead.Close()
	outRead, outWrite, err := os.Pipe()
	if err != nil {
		inWrite.Close()
		return nil, err
	}
	defer outWrite.Close()
	c = &Console{Input: inWrite, Output: outRead, cols: cols, rows: rows}
	if err = windows.CreatePseudoConsole(windows.Coord{X: int16(cols), Y: int16(rows)}, windows.Handle(inRead.Fd()), windows.Handle(outWrite.Fd()), 0, &c.handle); err != nil {
		inWrite.Close()
		outRead.Close()
		return nil, err
	}
	defer func(console *Console) {
		if err != nil {
			console.Close()
		}
	}(c)
	attrs, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return nil, err
	}
	defer attrs.Delete()
	// This attribute takes HPCON itself. Pass the raw handle to Win32 instead
	// of inventing a Go pointer, which would be invalid for the GC/checkptr.
	ok, _, callErr := windows.NewLazySystemDLL("kernel32.dll").NewProc("UpdateProcThreadAttribute").Call(
		uintptr(unsafe.Pointer(attrs.List())), 0, windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE,
		uintptr(c.handle), unsafe.Sizeof(c.handle), 0, 0)
	if ok == 0 {
		return nil, errors.Join(errors.New("cannot associate ConPTY with process startup"), callErr)
	}
	si := windows.StartupInfoEx{}
	si.Cb = uint32(unsafe.Sizeof(si))
	si.ProcThreadAttributeList = attrs.List()
	executable, err := windows.UTF16PtrFromString(cmd.Path)
	if err != nil {
		return nil, err
	}
	args := make([]string, len(cmd.Args))
	for i, a := range cmd.Args {
		args[i] = windows.EscapeArg(a)
	}
	line, err := windows.UTF16PtrFromString(strings.Join(args, " "))
	if err != nil {
		return nil, err
	}
	var cwd *uint16
	if cmd.Dir != "" {
		cwd, err = windows.UTF16PtrFromString(cmd.Dir)
		if err != nil {
			return nil, err
		}
	}
	env := cmd.Env
	if env == nil {
		env = os.Environ()
	}
	env = append([]string{}, env...)
	for _, item := range env {
		if strings.ContainsRune(item, 0) {
			return nil, errors.New("Windows environment contains a NUL byte")
		}
	}
	sort.Slice(env, func(i, j int) bool { return strings.ToUpper(env[i]) < strings.ToUpper(env[j]) })
	block := utf16.Encode([]rune(strings.Join(env, "\x00") + "\x00\x00"))
	pi := windows.ProcessInformation{}
	flags := uint32(windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_UNICODE_ENVIRONMENT | windows.CREATE_SUSPENDED)
	if err = windows.CreateProcess(executable, line, nil, nil, false, flags, &block[0], cwd, &si.StartupInfo, &pi); err != nil {
		return nil, err
	}
	defer windows.CloseHandle(pi.Thread)
	defer windows.CloseHandle(pi.Process)
	cmd.Process, err = os.FindProcess(int(pi.ProcessId))
	if err != nil {
		windows.TerminateProcess(pi.Process, 125)
		return nil, err
	}
	return c, nil
}
func (c *Console) Resize(cols, rows uint16) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if cols == 0 {
		cols = c.cols
	}
	if rows == 0 {
		rows = c.rows
	}
	if c.handle == 0 {
		return os.ErrClosed
	}
	if err := windows.ResizePseudoConsole(c.handle, windows.Coord{X: int16(cols), Y: int16(rows)}); err != nil {
		return err
	}
	c.cols, c.rows = cols, rows
	return nil
}

// ClosePseudoConsole may wait while flushing output. Drain output concurrently;
// callers close only after their Job Object has terminated every workload.
func (c *Console) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Input.Close()
	if c.handle != 0 {
		windows.ClosePseudoConsole(c.handle)
		c.handle = 0
	}
	c.Output.Close()
}
