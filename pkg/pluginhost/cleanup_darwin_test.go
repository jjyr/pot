package pluginhost

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestCloseWaitsForZombieDescendantReaping(t *testing.T) {
	for _, reap := range []bool{true, false} {
		t.Run(map[bool]string{true: "reaped", false: "unreaped"}[reap], func(t *testing.T) {
			testCloseWithZombie(t, reap)
		})
	}
}

func testCloseWithZombie(t *testing.T, reap bool) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	c, err := New(Config{Node: node, Identity: "cleanup-fixture", Entrypoint: "index.mjs", Mode: "text", Files: map[string]string{
		"index.mjs": `export function createPlugin() { return {async execute(){}, async dispose(){}}; }`,
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close(); _ = os.RemoveAll(c.directory) }()
	if result := execute(t, c, map[string]any{}); result.IsError {
		t.Fatalf("plugin startup: %+v", result)
	}
	child := exec.Command("/usr/bin/true")
	child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pgid: c.cmd.Process.Pid}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer child.Wait()
	deadline := time.Now().Add(5 * time.Second)
	for {
		info, err := unix.SysctlKinfoProc("kern.proc.pid", child.Process.Pid)
		if err != nil {
			t.Fatal(err)
		}
		if info.Proc.P_stat == 5 { // Darwin SZOMB (sys/proc.h).
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("child did not become a zombie")
		}
		time.Sleep(time.Millisecond)
	}
	if !reap {
		err := c.Close()
		if !errors.Is(err, syscall.EPERM) || !errors.Is(err, syscall.ETIMEDOUT) {
			t.Fatalf("unreaped group cleanup = %v, want permission error and deadline", err)
		}
		if _, err := os.Stat(c.directory); err != nil {
			t.Fatalf("unconfirmed cleanup removed plugin directory: %v", err)
		}
		return
	}
	reaped := make(chan error, 1)
	go func() {
		// Wait for the plugin leader to exit, leaving only the zombie member.
		for time.Now().Before(deadline) {
			if errors.Is(syscall.Kill(-c.cmd.Process.Pid, 0), syscall.EPERM) {
				break
			}
			time.Sleep(time.Millisecond)
		}
		time.Sleep(50 * time.Millisecond)
		reaped <- child.Wait()
	}()
	err = c.Close()
	if waitErr := <-reaped; waitErr != nil {
		t.Fatal(waitErr)
	}
	if err != nil {
		t.Fatalf("plugin cleanup after zombie reaping: %v", err)
	}
	if _, err = os.Stat(c.directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("plugin directory retained after confirmed cleanup: %v", err)
	}
}
