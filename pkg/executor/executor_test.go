package executor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func fixture(t *testing.T) (*Engine, Request) {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	policy := DefaultShellSettings()
	policy.ShellSnapshot = false
	policy.AllowLoginShell = false
	policy.DefaultYieldMS = 1000
	policy.MaxYieldMS = 1000
	e := New(Options{Home: dir})
	t.Cleanup(func() {
		if err := e.Close(); err != nil {
			t.Errorf("cleanup: %v", err)
		}
	})
	return e, Request{Mode: "host", ID: "call", CWD: dir, Policy: policy}
}
func call(t *testing.T, e *Engine, r Request, name string, args map[string]any) map[string]any {
	t.Helper()
	r.Operation = name
	if name == "exec_command" {
		args["interactive"] = true
	}
	r.Args = args
	result, err := e.Execute(context.Background(), r, nil)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestProductHostAdmissionPrecedesStartupAndSanitizesEnvironment(t *testing.T) {
	e, r := fixture(t)
	r.Policy.AllowLoginShell = true
	r.Policy.ShellSnapshot = true
	r.Operation = "exec_command"
	r.Args = map[string]any{"cmd": "printf should-not-run"}
	rejected := errors.New("policy changed")
	e.options.Admit = func(uint64) (func(), error) { return nil, rejected }
	if _, err := e.Execute(context.Background(), r, nil); !errors.Is(err, rejected) {
		t.Fatal(err)
	}
	if len(e.snapshots.entries) != 0 {
		t.Fatal("product ran login snapshot before final admission")
	}
	e.options.Admit = func(uint64) (func(), error) { return func() {}, nil }
	t.Setenv("POT_TEST_AUTH_SECRET", "private")
	r.Policy.AllowLoginShell = false
	// A fresh execution owns immutable shell settings.
	e2 := New(e.options)
	defer e2.Close()
	r.Args = map[string]any{"cmd": "printf '%s' \"${POT_TEST_AUTH_SECRET-unset}\""}
	result, err := e2.Execute(context.Background(), r, nil)
	if err != nil || result["stdout"] != "unset" {
		t.Fatal("ambient authority inherited", result, err)
	}
}

func TestSandboxPreparationFailureReportsNoStartedWorkload(t *testing.T) {
	e, r := fixture(t)
	r.Mode = "sandbox"
	r.Operation = "exec_command"
	r.Args = map[string]any{"cmd": "touch should-not-exist"}
	result, err := e.Execute(context.Background(), r, nil)
	var preparation *PreparationError
	if !errors.As(err, &preparation) || result["execution_mode"] != "sandbox" || result["execution_status"] != "preparation_failed" {
		t.Fatal(result, err)
	}
	if _, err := os.Stat(filepath.Join(r.CWD, "should-not-exist")); !os.IsNotExist(err) {
		t.Fatal("failed preparation ran command", err)
	}
}

func TestHostInteractiveAuthorityDoesNotComeFromOrdinaryCommand(t *testing.T) {
	e, r := fixture(t)
	r.Operation = "exec_command"
	r.Args = map[string]any{"cmd": "read -r value; echo received:$value", "yield_time_ms": 10}
	result, err := e.Execute(context.Background(), r, nil)
	if err != nil {
		t.Fatal(err)
	}
	id := result["session_id"]
	if id == nil {
		t.Fatal(result)
	}
	r.Mode = "session"
	r.Operation = "write_stdin"
	r.Args = map[string]any{"session_id": id, "chars": "unexpected\\n", "yield_time_ms": 10}
	if _, err = e.Execute(context.Background(), r, nil); err == nil {
		t.Fatal("ordinary host command accepted interactive input")
	}
	r.Args = map[string]any{"session_id": id, "terminate": true, "yield_time_ms": 1000}
	if _, err = e.Execute(context.Background(), r, nil); err != nil {
		t.Fatal("owned cancellation denied", err)
	}
}
func TestSeparateCommandsAndBoundedUTF8Output(t *testing.T) {
	e, r := fixture(t)
	os.Mkdir(filepath.Join(r.CWD, "dir"), 0700)
	call(t, e, r, "exec_command", map[string]any{"cmd": "cd dir; export POT_EXEC_TEST=changed"})
	result := call(t, e, r, "exec_command", map[string]any{"cmd": "pwd; printf '%s' \"${POT_EXEC_TEST-unset}\"; printf err >&2"})
	if !strings.Contains(result["stdout"].(string), r.CWD) || !strings.HasSuffix(result["stdout"].(string), "unset") || result["stderr"] != "err" || result["exit_code"] != 0 {
		t.Fatal(result)
	}
	r.Operation = "exec_command"
	r.Args = map[string]any{"cmd": "printf '\344'; sleep .02; printf '\270'; sleep .02; printf '\255 🌱 done'"}
	var streamed strings.Builder
	result, err := e.Execute(context.Background(), r, func(event Event) error { streamed.WriteString(event.ToolOutput.Text); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if result["stdout"] != "中 🌱 done" || streamed.String() != result["stdout"] {
		t.Fatalf("split UTF8 output %v %q", result, streamed.String())
	}
	result = call(t, e, r, "exec_command", map[string]any{"cmd": "yes x | head -c 100000", "max_output_chars": 64})
	if len(result["stdout"].(string)) > 64 || result["truncated"] != true {
		t.Fatal(result)
	}
	if _, err := e.Execute(context.Background(), Request{ID: r.ID, CWD: r.CWD, Policy: r.Policy, Operation: "exec_command", Args: map[string]any{"cmd": "pwd", "workdir": "../"}}, nil); err == nil {
		t.Fatal("accepted parent path")
	}
}
func TestPipePTYResizeInterruptAndFinishedRead(t *testing.T) {
	e, r := fixture(t)
	result := call(t, e, r, "exec_command", map[string]any{"cmd": "read -r line; printf 'received=%s' \"$line\"", "yield_time_ms": 10})
	id := result["session_id"].(string)
	bad := r
	bad.Operation = "write_stdin"
	bad.Args = map[string]any{"session_id": id, "rows": 40}
	if _, err := e.Execute(context.Background(), bad, nil); err == nil {
		t.Fatal("resized pipe")
	}
	result = call(t, e, r, "write_stdin", map[string]any{"session_id": id, "chars": "hello\n", "yield_time_ms": 500})
	if result["stdout"] != "received=hello" || result["session_id"] != nil {
		t.Fatal(result)
	}
	result = call(t, e, r, "exec_command", map[string]any{"cmd": "test -t 0 && echo terminal; stty size; read -r line; stty size; printf 'input=%s\n' \"$line\"; sleep 60", "tty": true, "yield_time_ms": 30})
	id = result["session_id"].(string)
	output := result["stdout"].(string)
	result = call(t, e, r, "write_stdin", map[string]any{"session_id": id, "rows": 40, "cols": 100, "chars": "hello\n", "yield_time_ms": 100})
	output += result["stdout"].(string)
	if !strings.Contains(output, "terminal") || !strings.Contains(output, "24 80") || !strings.Contains(output, "40 100") || !strings.Contains(output, "input=hello") {
		t.Fatalf("PTY: %q", output)
	}
	result = call(t, e, r, "write_stdin", map[string]any{"session_id": id, "interrupt": true, "yield_time_ms": 500})
	if result["session_id"] != nil {
		t.Fatalf("interrupt did not settle: %v", result)
	}
	result = call(t, e, r, "exec_command", map[string]any{"cmd": "sleep .05; printf late", "yield_time_ms": 10})
	id = result["session_id"].(string)
	time.Sleep(100 * time.Millisecond)
	result = call(t, e, r, "write_stdin", map[string]any{"session_id": id, "yield_time_ms": 100})
	if result["stdout"] != "late" || result["session_id"] != nil {
		t.Fatal(result)
	}
}
func TestTimeoutIdleCancellationAndNoDescendants(t *testing.T) {
	e, r := fixture(t)
	r.Operation = "exec_command"
	r.Args = map[string]any{"cmd": "touch should-not-start", "timeout_ms": 30}
	if _, err := e.Execute(context.Background(), r, nil); err == nil {
		t.Fatal("accepted removed hard timeout parameter")
	}
	if _, err := os.Stat(filepath.Join(r.CWD, "should-not-start")); !os.IsNotExist(err) {
		t.Fatal("invalid command started", err)
	}
	r.Policy.SessionIdleTimeoutMS = 100
	// A changed scope policy is intentionally rejected; expiry gets a fresh scope.
	e2 := New(Options{Home: r.CWD})
	defer e2.Close()
	result := call(t, e2, r, "exec_command", map[string]any{"cmd": "sleep 60", "yield_time_ms": 10})
	id := result["session_id"].(string)
	time.Sleep(180 * time.Millisecond)
	result = call(t, e2, r, "write_stdin", map[string]any{"session_id": id, "yield_time_ms": 30})
	if result["reason"] != "idle_timeout" || result["session_id"] != nil {
		t.Fatal(result)
	}
	r.Policy.SessionIdleTimeoutMS = 1800000
	e3 := New(Options{Home: r.CWD})
	defer e3.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	r.Operation = "exec_command"
	r.Args = map[string]any{"cmd": "sleep 60 & echo $! > child.pid; wait"}
	if _, err := e3.Execute(ctx, r, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(r.CWD, "child.pid"))
	if err != nil {
		t.Fatal(err)
	}
	var pid int
	fmt.Sscanf(string(data), "%d", &pid)
	for range 100 {
		if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("cancelled descendant %d remains", pid)
}
func TestConcurrentCommandsAndDrain(t *testing.T) {
	e, r := fixture(t)
	var group sync.WaitGroup
	for range 4 {
		group.Go(func() {
			request := r
			request.Operation = "exec_command"
			request.Args = map[string]any{"cmd": "sleep .05; printf done"}
			result, err := e.Execute(context.Background(), request, nil)
			if err != nil || result["stdout"] != "done" {
				t.Errorf("%v %v", result, err)
			}
		})
	}
	group.Wait()
	result := call(t, e, r, "exec_command", map[string]any{"cmd": "sleep 60", "yield_time_ms": 10})
	if result["session_id"] == nil {
		t.Fatal(result)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	inventory, err := e.Inspect()
	if err != nil || inventory["active"] != false {
		t.Fatalf("%v %v", inventory, err)
	}
	if _, err := e.Execute(context.Background(), r, nil); err == nil {
		t.Fatal("closed scope reused")
	}
}
func TestLoginSnapshotReuseInvalidationAndNativeFunctions(t *testing.T) {
	e, r := fixture(t)
	r.Policy.AllowLoginShell = true
	r.Policy.ShellSnapshot = true
	profile := filepath.Join(r.CWD, ".bash_profile")
	source := "printf x >> \"$HOME/captures\"\nexport POT_BASELINE=first\npot_function() { printf function-ok; }\n"
	if err := os.WriteFile(profile, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	result := call(t, e, r, "exec_command", map[string]any{"cmd": "pot_function; printf ':%s' \"$POT_BASELINE\""})
	if result["stdout"] != "function-ok:first" {
		t.Fatal(result)
	}
	call(t, e, r, "exec_command", map[string]any{"cmd": "export POT_BASELINE=changed"})
	result = call(t, e, r, "exec_command", map[string]any{"cmd": "printf '%s' \"$POT_BASELINE\""})
	if result["stdout"] != "first" {
		t.Fatal(result)
	}
	data, err := os.ReadFile(filepath.Join(r.CWD, "captures"))
	if err != nil || string(data) != "x" {
		t.Fatalf("capture reuse %q %v", data, err)
	}
	os.WriteFile(profile, []byte(strings.ReplaceAll(source, "first", "second")), 0600)
	result = call(t, e, r, "exec_command", map[string]any{"cmd": "printf '%s' \"$POT_BASELINE\""})
	if result["stdout"] != "second" {
		t.Fatal(result)
	}
}

func TestFailedSnapshotFallbackAndZshAliases(t *testing.T) {
	e, r := fixture(t)
	r.Policy.AllowLoginShell = true
	r.Policy.ShellSnapshot = true
	profile := "printf x >> \"$HOME/captures\"\npot_large() { printf '" + strings.Repeat("x", 70000) + "'; }\n"
	if err := os.WriteFile(filepath.Join(r.CWD, ".bash_profile"), []byte(profile), 0600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		result := call(t, e, r, "exec_command", map[string]any{"cmd": "printf fallback-ok"})
		if result["stdout"] != "fallback-ok" {
			t.Fatal(result)
		}
	}
	data, err := os.ReadFile(filepath.Join(r.CWD, "captures"))
	if err != nil || string(data) != "xxx" {
		t.Fatalf("failed snapshot should be cached %q %v", data, err)
	}
	if _, err := os.Stat("/bin/zsh"); err != nil {
		return
	}
	e2 := New(Options{Home: r.CWD})
	defer e2.Close()
	os.WriteFile(filepath.Join(r.CWD, ".zprofile"), []byte("printf x >> \"$HOME/z-captures\"\nexport POT_ZBASE=ready\npot_zfunction() { printf function; }\nalias pot_zalias='printf alias'\numask 077\n"), 0600)
	for range 2 {
		result := call(t, e2, r, "exec_command", map[string]any{"shell": "/bin/zsh", "cmd": "printf '%s:' \"$POT_ZBASE\"; pot_zfunction; pot_zalias; printf ':'; umask"})
		if result["stdout"] != "ready:functionalias:077\n" {
			t.Fatal(result)
		}
	}
	data, err = os.ReadFile(filepath.Join(r.CWD, "z-captures"))
	if err != nil || string(data) != "x" {
		t.Fatalf("zsh reuse %q %v", data, err)
	}
	if err := e2.Drain(); err != nil {
		t.Fatal(err)
	}
	result := call(t, e2, r, "exec_command", map[string]any{"shell": "/bin/zsh", "cmd": "printf '%s' \"$POT_ZBASE\""})
	if result["stdout"] != "ready" {
		t.Fatal(result)
	}
	data, _ = os.ReadFile(filepath.Join(r.CWD, "z-captures"))
	if string(data) != "x" {
		t.Fatal("normal turn drainage discarded login cache")
	}
}

func TestBlockedPTYInputIsBoundedAndCancelled(t *testing.T) {
	e, r := fixture(t)
	r.Policy.MaxYieldMS = 100
	r.Policy.DefaultYieldMS = 100
	result := call(t, e, r, "exec_command", map[string]any{"cmd": "stty -icanon -echo; printf ready; sleep 60", "tty": true, "yield_time_ms": 30})
	id := result["session_id"].(string)
	r.Operation = "write_stdin"
	r.Args = map[string]any{"session_id": id, "chars": strings.Repeat("x", 1024*1024), "yield_time_ms": 10}
	start := time.Now()
	if _, err := e.Execute(context.Background(), r, nil); err == nil {
		t.Fatal("large input unexpectedly drained into unread PTY")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("blocked terminal write prevented cancellation")
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultYieldKeepsCommandAliveUntilPolled(t *testing.T) {
	e, r := fixture(t)
	r.Policy.DefaultYieldMS = 10
	result := call(t, e, r, "exec_command", map[string]any{"cmd": "sleep .1; printf completed"})
	id, ok := result["session_id"].(string)
	if !ok || result["execution_status"] != "running" || result["reason"] != nil {
		t.Fatal("default wait did not yield a live command", result)
	}
	result = call(t, e, r, "write_stdin", map[string]any{"session_id": id, "yield_time_ms": 1000})
	if result["stdout"] != "completed" || result["exit_code"] != 0 || result["session_id"] != nil {
		t.Fatal("default wait killed or lost the original process", result)
	}
}

func TestCommandShellPreservesToolchainPATH(t *testing.T) {
	for _, mode := range []string{"product", "standalone", "snapshot"} {
		for _, login := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/login=%v", mode, login), func(t *testing.T) {
				e, r := fixture(t)
				bin := filepath.Join(r.CWD, "runtime 'quoted'", "bin")
				project := filepath.Join(r.CWD, "project-bin")
				for dir, value := range map[string]string{bin: "packaged", project: "project"} {
					if err := os.MkdirAll(dir, 0700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(dir, "node"), []byte("#!/bin/sh\nprintf "+value), 0700); err != nil {
						t.Fatal(err)
					}
				}
				e.options.Node = filepath.Join(bin, "node")
				if mode == "product" {
					e.options.Admit = func(uint64) (func(), error) { return func() {}, nil }
				}
				r.Policy.ShellSnapshot = mode == "snapshot"
				r.Policy.AllowLoginShell = login
				if login {
					if err := os.WriteFile(filepath.Join(r.CWD, ".bash_profile"), []byte("export PATH="+project+":/usr/bin:/bin\n"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				t.Setenv("PATH", project+":/usr/bin:/bin")
				result := call(t, e, r, "exec_command", map[string]any{"cmd": "node"})
				if result["stdout"] != "project" || result["exit_code"] != 0 {
					t.Fatal(result)
				}
				result = call(t, e, r, "exec_command", map[string]any{"cmd": filepath.Join(project, "node")})
				if result["stdout"] != "project" || result["exit_code"] != 0 {
					t.Fatal(result)
				}
			})
		}
	}
}
