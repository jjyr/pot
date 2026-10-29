package processscope

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestMain(m *testing.M) {
	if HandleWorker() {
		return
	}
	os.Exit(m.Run())
}

func TestWorkingDirectory(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	inherited, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, directory, expected string
		invalid                   bool
	}{
		{name: "inherited", expected: inherited},
		{name: "explicit", directory: directory, expected: directory},
		{name: "missing", directory: filepath.Join(directory, "missing"), invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			command := exec.Command("/bin/pwd")
			command.Dir = tc.directory
			command.Env = os.Environ()
			var output bytes.Buffer
			command.Stdout = &output
			scope, err := Prepare(command, []string{binary}, Markers{})
			if err != nil {
				t.Fatal(err)
			}
			if err = command.Start(); err != nil {
				scope.Abort()
				if tc.invalid && errors.Is(err, os.ErrNotExist) {
					return
				}
				t.Fatal(err)
			}
			if err = scope.Started(); err != nil {
				t.Fatal(err)
			}
			waitErr := command.Wait()
			if err = scope.Finish(); err != nil {
				t.Fatal(err)
			}
			if tc.invalid {
				t.Fatal("missing working directory was accepted")
			}
			if waitErr != nil {
				t.Fatal(waitErr)
			}
			if got := strings.TrimSpace(output.String()); got != tc.expected {
				t.Fatalf("working directory = %q, want %q", got, tc.expected)
			}
		})
	}
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

func TestSupervisorReadinessPrecedesImmediateCancellation(t *testing.T) {
	testSupervisorStartupCancellation(t, false)
}

func TestSupervisorCancellationBeforeReadinessDoesNotLaunch(t *testing.T) {
	testSupervisorStartupCancellation(t, true)
}

func testSupervisorStartupCancellation(t *testing.T, beforeReady bool) {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	gatePath := filepath.Join(dir, "gate")
	if err := unix.Mkfifo(gatePath, 0600); err != nil {
		t.Fatal(err)
	}
	gate, err := os.OpenFile(gatePath, os.O_RDWR|syscall.O_NONBLOCK, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Close()
	marker := filepath.Join(dir, "entered")
	wrapper := []string{"/bin/sh", "-c", `printf entered > "$1"; read -r release < "$2"; shift 2; exec "$@"`, "wrapper", marker, gatePath, binary}
	launched := filepath.Join(dir, "workload-started")
	command := exec.Command("/bin/sh", "-c", `printf launched > "$1"; sleep 90`, "workload", launched)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Env = []string{"PATH=/usr/bin:/bin"}
	scope, err := Prepare(command, wrapper, Markers{})
	if err != nil {
		t.Fatal(err)
	}
	if err = command.Start(); err != nil {
		scope.Abort()
		t.Fatal(err)
	}
	waited := false
	defer func() {
		if !waited {
			_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
			_ = command.Wait()
		}
		scope.Abort()
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, err := os.Stat(marker)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrNotExist) || time.Now().After(deadline) {
			t.Fatalf("supervisor launch gate: %v", err)
		}
		time.Sleep(time.Millisecond)
	}
	started := make(chan error, 1)
	go func() { started <- scope.Started() }()
	// The wrapper is alive, but the real supervisor cannot yet install its
	// signal handler. Writing launch configuration does not establish readiness.
	select {
	case err := <-started:
		t.Fatalf("Started returned before supervisor readiness: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	var stopped chan error
	if beforeReady {
		stopped = make(chan error, 1)
		go func() { stopped <- scope.Stop(command.Process.Pid) }()
		// Observe the cancellation decision under the same lock as Started; gate
		// release cannot race ahead of cancellation merely due to scheduling.
		deadline := time.Now().Add(time.Second)
		for {
			scope.startupMu.Lock()
			requested := scope.stopRequested
			scope.startupMu.Unlock()
			if requested {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("early cancellation was not recorded")
			}
			time.Sleep(time.Millisecond)
		}
		select {
		case err := <-stopped:
			t.Fatalf("cancellation completed before the startup decision: %v", err)
		default:
		}
	}
	if _, err = fmt.Fprint(gate, "go\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-started:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("supervisor readiness did not settle")
	}
	if beforeReady {
		select {
		case err = <-stopped:
		case <-time.After(6 * time.Second):
			t.Fatal("early cancellation did not settle")
		}
	} else {
		err = scope.Stop(command.Process.Pid)
	}
	if err != nil {
		t.Fatal(err)
	}
	_ = command.Wait()
	waited = true
	if err = scope.Finish(); err != nil {
		t.Fatalf("immediate cancellation lost cleanup proof: %v", err)
	}
	if beforeReady {
		if _, err := os.Stat(launched); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("cancelled startup executed workload: %v", err)
		}
	}
}

func TestSupervisorReadinessIsNotCleanupProof(t *testing.T) {
	for _, tc := range []struct {
		name, wire string
		ready      bool
	}{
		{name: "missing readiness"},
		{name: "cleanup without readiness", wire: `{"clean":true}`},
		{name: "ready without cleanup", wire: "R", ready: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			command := exec.Command("/bin/true")
			command.Env = []string{"PATH=/usr/bin:/bin"}
			// A protocol fixture closes proof independently of configuration, so EOF
			// cannot be mistaken for either startup permission or cleanup evidence.
			worker := []string{"/bin/sh", "-c", `IFS= read -r config <&3; printf '%s' "$1" >&4; exec 4>&-; dd bs=1 count=1 <&3 >/dev/null 2>&1`, "fixture", tc.wire}
			scope, err := Prepare(command, worker, Markers{})
			if err != nil {
				t.Fatal(err)
			}
			defer scope.Abort()
			if err = command.Start(); err != nil {
				t.Fatal(err)
			}
			err = scope.Started()
			if stopErr := scope.Stop(command.Process.Pid); stopErr != nil {
				t.Error(stopErr)
			}
			_ = command.Wait()
			if (err == nil) != tc.ready {
				t.Fatalf("readiness error=%v, want ready=%v", err, tc.ready)
			}
			if err = scope.Finish(); err == nil {
				t.Fatal("startup bytes accepted as cleanup proof")
			}
		})
	}
}

func TestSupervisorAbortReleasesPendingCancellation(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command("/bin/true")
	scope, err := Prepare(command, []string{binary}, Markers{})
	if err != nil {
		t.Fatal(err)
	}
	if err = command.Start(); err != nil {
		scope.Abort()
		t.Fatal(err)
	}
	defer func() { _ = command.Process.Kill(); _ = command.Wait(); scope.Abort() }()
	stopped := make(chan error, 1)
	go func() { stopped <- scope.Stop(command.Process.Pid) }()
	// No Started call will run: Abort must settle cancellation even on launch
	// failure paths, without authorizing the workload or inventing cleanup proof.
	scope.Abort()
	select {
	case err = <-stopped:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Abort left cancellation blocked")
	}
	if err = scope.Finish(); err == nil {
		t.Fatal("Abort invented cleanup proof")
	}
}

func TestSupervisorStartupDecisionPreservesJSONFraming(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "workload-started")
	command := exec.Command("/bin/sh", "-c", `touch "$1"`, "workload", marker)
	command.Env = []string{"PADDING=" + strings.Repeat("x", 128*1024)}
	scope, err := Prepare(command, []string{binary}, Markers{})
	if err != nil {
		t.Fatal(err)
	}
	if err = command.Start(); err != nil {
		scope.Abort()
		t.Fatal(err)
	}
	defer func() { _ = command.Process.Kill(); _ = command.Wait(); scope.Abort() }()
	for _, file := range scope.childFiles {
		_ = file.Close()
	}
	scope.childFiles = nil
	payload, err := json.Marshal(scope.payload)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(startupTimeout)
	if err = scope.config.SetWriteDeadline(deadline); err != nil {
		t.Fatal(err)
	}
	if err = scope.proof.SetReadDeadline(deadline); err != nil {
		t.Fatal(err)
	}
	if _, err = scope.config.Write(payload); err != nil {
		t.Fatal(err)
	}
	var ready [1]byte
	if _, err = io.ReadFull(scope.proof, ready[:]); err != nil || ready[0] != 'R' {
		t.Fatal("readiness", ready, err)
	}
	// Deliver the launch JSON's trailing whitespace only after its decoder has
	// returned and emitted ready. A discarded decoder plus raw ACK read fails.
	if _, err = io.WriteString(scope.config, "\n\"C\"\n"); err != nil {
		t.Fatal(err)
	}
	_ = scope.config.Close()
	_ = command.Wait()
	if err = scope.Finish(); err != nil {
		t.Fatalf("fragmented startup framing lost cleanup proof: %v", err)
	}
	if _, err = os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled startup ran workload: %v", err)
	}
}

func TestStartupDeadlineBoundsConfigurationAndReadiness(t *testing.T) {
	for _, large := range []bool{false, true} {
		name := "readiness"
		if large {
			name = "configuration"
		}
		t.Run(name, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "workload-started")
			command := exec.Command("/bin/sh", "-c", `touch "$1"`, "workload", marker)
			command.Env = []string{"PATH=/usr/bin:/bin"}
			if large {
				command.Env = append(command.Env, "PADDING="+strings.Repeat("x", 1024*1024))
			}
			// A live worker holds both private descriptors without reading config or
			// sending ready. Small config fills neither pipe; large config blocks writes.
			scope, err := Prepare(command, []string{"/bin/sh", "-c", "exec sleep 90", "fixture"}, Markers{})
			if err != nil {
				t.Fatal(err)
			}
			if err = command.Start(); err != nil {
				scope.Abort()
				t.Fatal(err)
			}
			defer func() { _ = command.Process.Kill(); _ = command.Wait(); scope.Abort() }()
			started := make(chan error, 1)
			go func() { started <- scope.Started() }()
			select {
			case err = <-started:
				if !errors.Is(err, os.ErrDeadlineExceeded) {
					t.Fatalf("startup was not bounded by its deadline: %v", err)
				}
			case <-time.After(2 * startupTimeout):
				t.Fatal("startup remained blocked beyond its deadline")
			}
			if err = scope.Stop(command.Process.Pid); err != nil {
				t.Fatal(err)
			}
			_ = command.Wait()
			if err = scope.Finish(); err == nil {
				t.Fatal("timed-out startup invented cleanup proof")
			}
			if _, err = os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("timed-out startup ran workload: %v", err)
			}
		})
	}
}
