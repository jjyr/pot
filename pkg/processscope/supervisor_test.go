package processscope

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if HandleWorker() {
		return
	}
	os.Exit(m.Run())
}
func TestDetachedDescendantIsStoppedBeforeReceipt(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	command := exec.Command(node, "-e", `const fs=require('node:fs');const child=require('node:child_process').spawn('/bin/sleep',['90'],{detached:true,stdio:'ignore'});fs.writeFileSync('childpid',String(child.pid));child.unref();`)
	command.Dir = directory
	command.Env = os.Environ()
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	scope, err := Prepare(command, []string{binary}, Markers{})
	if err != nil {
		t.Fatal(err)
	}
	if err = command.Start(); err != nil {
		scope.Abort()
		t.Fatal(err)
	}
	if err = scope.Started(); err != nil {
		t.Fatal(err)
	}
	if err = command.Wait(); err != nil {
		t.Fatal(err)
	}
	if err = scope.Finish(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(directory, "childpid"))
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	if err = syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		t.Fatalf("detached descendant survived: %v", err)
	}
}
func TestCancellationAndMissingReceiptAreDistinct(t *testing.T) {
	binary, _ := os.Executable()
	for _, force := range []bool{false, true} {
		command := exec.Command("/bin/sh", "-c", "sleep 90")
		command.Dir = t.TempDir()
		command.Env = os.Environ()
		command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		scope, err := Prepare(command, []string{binary}, Markers{})
		if err != nil {
			t.Fatal(err)
		}
		if err = command.Start(); err != nil {
			scope.Abort()
			t.Fatal(err)
		}
		if err = scope.Started(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(100 * time.Millisecond)
		if force {
			_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		} else {
			_ = scope.Stop(command.Process.Pid)
		}
		_ = command.Wait()
		err = scope.Finish()
		if force && err == nil {
			t.Fatal("missing proof accepted")
		}
		if !force && err != nil {
			t.Fatal(err)
		}
	}
}
