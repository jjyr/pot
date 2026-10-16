//go:build darwin || linux

package instance

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The helper is a real separate Runtime owner. Its external shell/sleep child
// survives SIGKILL, so the test exercises descriptor inheritance across exec,
// rather than Go's ordinary os.OpenFile descriptor defaults or a graceful Close.
func TestRuntimeLockCrashHelper(t *testing.T) {
	root := os.Getenv("POT_ROOT_LOCK_TEST_ROOT")
	if root == "" {
		return
	}
	owner, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	child := exec.Command("/bin/sh", "-c", `echo $$ > "$1"; exec sleep 60`, "root-lock-child", os.Getenv("POT_ROOT_LOCK_TEST_PID"))
	if err = child.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Hour)
}
func TestRuntimeLockDoesNotSurviveKilledOwnerInExternalChild(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "runtime")
	pidPath := filepath.Join(base, "child.pid")
	command := exec.Command(os.Args[0], "-test.run=^TestRuntimeLockCrashHelper$")
	command.Env = append(os.Environ(), "POT_ROOT_LOCK_TEST_ROOT="+root, "POT_ROOT_LOCK_TEST_PID="+pidPath)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if command.ProcessState == nil {
			_ = command.Process.Kill()
			_, _ = command.Process.Wait()
		}
	})
	deadline := time.Now().Add(5 * time.Second)
	var childPID int
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(pidPath); err == nil {
			childPID, _ = strconv.Atoi(strings.TrimSpace(string(data)))
			if childPID > 0 {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if childPID <= 0 {
		t.Fatal("external child did not publish its owned PID")
	}
	t.Cleanup(func() { _ = syscall.Kill(childPID, syscall.SIGKILL) })
	if other, err := Open(root); err == nil {
		other.Close()
		t.Fatal("live Runtime owner lost exclusivity")
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = command.Wait()
	if err := syscall.Kill(childPID, 0); err != nil {
		t.Fatal("external child did not survive owner SIGKILL", err)
	}
	reopened, err := Open(root)
	if err != nil {
		t.Fatalf("surviving external child inherited Runtime ownership: %v", err)
	}
	defer reopened.Close()
	if err = syscall.Kill(childPID, 0); err != nil {
		t.Fatal("test did not retain live external child during reopen", err)
	}
}
