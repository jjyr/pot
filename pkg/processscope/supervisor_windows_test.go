package processscope

import (
	"golang.org/x/sys/windows"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

func TestWindowsScopeHelper(t *testing.T) {
	mode := os.Getenv("POT_WINDOWS_SCOPE_TEST")
	if mode == "" {
		return
	}
	if mode == "sleep" {
		for {
			time.Sleep(time.Hour)
		}
	}
	binary, _ := os.Executable()
	child := exec.Command(binary, "-test.run=^TestWindowsScopeHelper$")
	child.Env = append(os.Environ(), "POT_WINDOWS_SCOPE_TEST=sleep")
	child.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
	if err := child.Start(); err != nil {
		os.Exit(125)
	}
	if err := os.WriteFile(os.Getenv("POT_WINDOWS_SCOPE_PID"), []byte(strconv.Itoa(child.Process.Pid)), 0600); err != nil {
		os.Exit(125)
	}
}
func TestWindowsSuspendedGateAndDetachedCleanup(t *testing.T) {
	binary, _ := os.Executable()
	file := filepath.Join(t.TempDir(), "child.pid")
	cmd := exec.Command(binary, "-test.run=^TestWindowsScopeHelper$")
	cmd.Env = append(os.Environ(), "POT_WINDOWS_SCOPE_TEST=spawn", "POT_WINDOWS_SCOPE_PID="+file)
	scope, err := Prepare(cmd, nil, Markers{})
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Abort()
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	time.Sleep(100 * time.Millisecond)
	if _, err = os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("workload ran before Job Object admission")
	}
	if err = scope.Started(); err != nil {
		t.Fatal(err)
	}
	if err = cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(string(body))
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(handle)
	if state, _ := windows.WaitForSingleObject(handle, 0); state != uint32(windows.WAIT_TIMEOUT) {
		t.Fatal("descendant did not outlive root")
	}
	if err = scope.Finish(); err != nil {
		t.Fatal(err)
	}
	if state, err := windows.WaitForSingleObject(handle, 1000); err != nil || state != windows.WAIT_OBJECT_0 {
		t.Fatalf("descendant survived cleanup: %v %v", state, err)
	}
}
