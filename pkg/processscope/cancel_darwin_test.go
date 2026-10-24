//go:build darwin && cgo

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

func TestDarwinCancellationLetsParentReapDetachedChild(t *testing.T) {
	testDarwinCancellation(t, detachedSleeper+`child.on('exit',()=>{fs.writeFileSync('reaped','yes');process.exit(0)});`, true)
}

func TestDarwinCancellationEscalatesUncooperativeParent(t *testing.T) {
	testDarwinCancellation(t, detachedSleeper+`child.on('exit',()=>fs.writeFileSync('reaped','yes'));`, true)
}

func TestDarwinCancellationWithNoDescendants(t *testing.T) {
	_, elapsed := testDarwinCancellation(t, `ready(process.pid);`, false)
	t.Logf("childless cancellation: %.3f ms", float64(elapsed.Microseconds())/1000)
	if elapsed >= darwinReapGrace {
		t.Fatalf("childless workload waited for the reaping grace: %v", elapsed)
	}
}

func TestDarwinCancellationPreservesParentExitHandling(t *testing.T) {
	testDarwinCancellation(t, detachedSleeper+`child.on('exit',()=>setTimeout(()=>{fs.writeFileSync('reaped','yes');process.exit(0)},50));`, true)
}

func TestDarwinCancellationReapsNestedDescendants(t *testing.T) {
	branch := cancellationFixture + detachedSleeper + `child.on('exit',()=>setTimeout(()=>{fs.writeFileSync('leaf-reaped','yes');process.exit(0)},50));`
	source := `const child=spawn(process.execPath,['-e',` + strconv.Quote(branch) + `],{stdio:'ignore'});child.on('exit',()=>{fs.writeFileSync('reaped','yes');process.exit(0)});`
	directory, elapsed := testDarwinCancellation(t, source, true)
	assertReapedWithinGrace(t, filepath.Join(directory, "leaf-reaped"), elapsed)
}

const cancellationFixture = `const fs=require('node:fs');const {spawn}=require('node:child_process');setInterval(()=>{},1000);function ready(pid){fs.writeFileSync('ready.tmp',String(pid));fs.renameSync('ready.tmp','ready');}`
const detachedSleeper = `const child=spawn('/bin/sleep',['90'],{detached:true,stdio:'ignore'});ready(child.pid);`

func testDarwinCancellation(t *testing.T, behavior string, descendant bool) (string, time.Duration) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	command := exec.Command(node, "-e", cancellationFixture+behavior)
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
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		_ = command.Wait()
		t.Fatal(err)
	}
	waited := make(chan struct{})
	go func() { _ = command.Wait(); close(waited) }()
	defer func() {
		select {
		case <-waited:
		default:
			_ = scope.Stop(command.Process.Pid)
			select {
			case <-waited:
			case <-time.After(2 * time.Second):
				_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
				<-waited
			}
		}
		_ = scope.Finish()
	}()
	var pid int
	deadline := time.Now().Add(5 * time.Second)
	for {
		body, readErr := os.ReadFile(filepath.Join(directory, "ready"))
		if readErr == nil {
			pid, err = strconv.Atoi(strings.TrimSpace(string(body)))
			if err != nil {
				t.Fatal(err)
			}
			break
		}
		if !errors.Is(readErr, os.ErrNotExist) || time.Now().After(deadline) {
			t.Fatalf("workload readiness: %v", readErr)
		}
		time.Sleep(time.Millisecond)
	}
	defer syscall.Kill(pid, syscall.SIGKILL)
	started := time.Now()
	if err = scope.Stop(command.Process.Pid); err != nil {
		t.Fatal(err)
	}
	select {
	case <-waited:
	case <-time.After(2 * time.Second):
		t.Fatal("cancellation did not stop the workload within its bound")
	}
	if err = scope.Finish(); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(started)
	if elapsed > 2*time.Second {
		t.Fatalf("cancellation was not bounded: %v", elapsed)
	}
	if err = syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("workload survived cleanup receipt: %v", err)
	}
	if descendant {
		assertReapedWithinGrace(t, filepath.Join(directory, "reaped"), elapsed)
	}
	return directory, elapsed
}

func assertReapedWithinGrace(t *testing.T, path string, elapsed time.Duration) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err == nil && string(body) == "yes" {
		return
	}
	// The bounded grace permits escalation when a cooperative parent cannot get
	// scheduled in time. Cleanup proof and descendant disappearance above remain
	// mandatory; a voluntary exit callback is required before that grace expires.
	if elapsed >= darwinReapGrace {
		t.Logf("parent exceeded reaping grace; forced cleanup completed in %v", elapsed)
		return
	}
	t.Fatalf("parent was stopped before its reaping grace: %q, %v", body, err)
}
