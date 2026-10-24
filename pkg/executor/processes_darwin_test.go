package executor

import (
	"errors"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestConfirmGroupGoneWaitsForZombieReaping(t *testing.T) {
	command := startZombie(t, 0)
	pid := command.Process.Pid
	if err := syscall.Kill(-pid, syscall.SIGKILL); !errors.Is(err, syscall.EPERM) {
		t.Fatalf("zombie group signal = %v, want EPERM", err)
	}
	reaped := make(chan error, 1)
	go func() {
		time.Sleep(50 * time.Millisecond)
		reaped <- command.Wait()
	}()
	err := confirmGroupGone(pid, time.Now().Add(5*time.Second))
	if waitErr := <-reaped; waitErr != nil {
		t.Fatal(waitErr)
	}
	if err != nil {
		t.Fatalf("reaped group cleanup: %v", err)
	}
}

func TestProcessRunSettlesAfterZombieSignalError(t *testing.T) {
	command := exec.Command("/bin/sh", "-c", "read line")
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer command.Wait()
	defer command.Process.Kill()
	child := startZombie(t, command.Process.Pid)
	reaped := make(chan error, 1)
	go func() {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if errors.Is(syscall.Kill(-command.Process.Pid, 0), syscall.EPERM) {
				break
			}
			time.Sleep(time.Millisecond)
		}
		time.Sleep(50 * time.Millisecond)
		reaped <- child.Wait()
	}()
	cleaned := false
	p := &process{cmd: command, done: make(chan struct{}), stop: make(chan struct{}), changed: make(chan struct{}, 1), touched: time.Now(), finishSandbox: func() error {
		cleaned = true
		return nil
	}}
	_ = input.Close()
	p.run(time.Minute)
	if waitErr := <-reaped; waitErr != nil {
		t.Fatal(waitErr)
	}
	if p.err != nil || !cleaned {
		t.Fatalf("execution did not settle after confirmed cleanup: %v, cleaned=%v", p.err, cleaned)
	}
}

func startZombie(t *testing.T, group int) *exec.Cmd {
	t.Helper()
	command := exec.Command("/usr/bin/true")
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pgid: group}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = command.Wait() })
	pid := command.Process.Pid
	deadline := time.Now().Add(5 * time.Second)
	for {
		info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
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
	return command
}
