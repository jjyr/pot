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

	"github.com/jjyr/pot/internal/testutil"
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
	// The deterministic tree test checks that grace is skipped. This real
	// process test checks bounded cancellation and confirmed disappearance;
	// elapsed time also includes native scans and runner scheduling.
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

func TestDarwinCancellationGraceDecisions(t *testing.T) {
	for _, initiallyChildless := range []bool{true, false} {
		t.Run(strconv.FormatBool(initiallyChildless), func(t *testing.T) {
			done := make(chan error)
			defer close(done)
			deadline := make(chan time.Time)
			tick := make(chan time.Time)
			scans := make(chan int)
			result := make(chan error, 1)
			leaves := make(chan nativeMember, 1)
			rootStopped := errors.New("root stopped")
			go func() {
				count := 0
				result <- cancelDarwinTree(1, func() ([]nativeMember, error) {
					count++
					select {
					case scans <- count:
					case <-done:
					}
					members := []nativeMember{{id: 1}}
					if !initiallyChildless && count == 1 {
						members = append(members, nativeMember{id: 2, parent: 1})
					}
					return members, nil
				}, func() error { return rootStopped }, func(member nativeMember) error {
					select {
					case leaves <- member:
					case <-done:
					}
					return nil
				}, done, deadline, tick)
			}()
			budget := time.NewTimer(testutil.Timeout(t, 5*time.Second))
			defer budget.Stop()
			awaitScan := func(want int) {
				t.Helper()
				select {
				case got := <-scans:
					if got != want {
						t.Fatalf("scan = %d, want %d", got, want)
					}
				case err := <-result:
					t.Fatalf("stopped parent before grace expired: %v", err)
				case <-budget.C:
					t.Fatal("tree scan did not run")
				}
			}
			awaitScan(1)
			if !initiallyChildless {
				select {
				case member := <-leaves:
					if member.id != 2 {
						t.Fatalf("stopped non-leaf: %+v", member)
					}
				case <-budget.C:
					t.Fatal("leaf was not stopped")
				}
				for scan := 2; scan <= 3; scan++ {
					select {
					case tick <- time.Time{}:
					case err := <-result:
						t.Fatalf("stopped parent before grace expired: %v", err)
					case <-budget.C:
						t.Fatal("tree did not wait for another scan")
					}
					awaitScan(scan)
				}
				close(deadline)
			}
			select {
			case err := <-result:
				if !errors.Is(err, rootStopped) {
					t.Fatalf("root stop result was lost: %v", err)
				}
			case <-budget.C:
				t.Fatal("tree did not stop at its decision boundary")
			}
			select {
			case member := <-leaves:
				t.Fatalf("unexpected leaf signal: %+v", member)
			default:
			}
		})
	}
}
