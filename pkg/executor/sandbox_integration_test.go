package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/testutil"
)

func sandboxFixture(t *testing.T) (*Engine, Request) {
	t.Helper()
	if os.Getenv("POT_SANDBOX_INTEGRATION") != "1" {
		t.Skip("set POT_SANDBOX_INTEGRATION=1 for real sandbox tests")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	directory, err := filepath.Abs("../../sandbox")
	if configured := os.Getenv("POT_SANDBOX_SDK"); configured != "" {
		directory = configured
	}
	if err != nil {
		t.Fatal(err)
	}
	worker, _ := os.Executable()
	e, r := fixture(t)
	e.options.Node = node
	e.options.SandboxDirectory = directory
	e.options.ProcessWorker = []string{worker}
	r.Mode = "sandbox"
	return e, r
}

// Collect only calls whose test requires completion. Interactive/cancellation
// probes keep their initial running session and opt in after sending input.
func completedSandboxResult(t *testing.T, e *Engine, r Request, result map[string]any) map[string]any {
	t.Helper()
	deadline := time.Now().Add(testutil.Timeout(t, 15*time.Second))
	var stdout, stderr strings.Builder
	truncated := false
	for {
		stdout.WriteString(result["stdout"].(string))
		stderr.WriteString(result["stderr"].(string))
		truncated = truncated || result["truncated"] == true
		if result["session_id"] == nil {
			result["stdout"], result["stderr"], result["truncated"] = stdout.String(), stderr.String(), truncated
			return result
		}
		if time.Now().After(deadline) {
			cleanupErr := e.Close()
			t.Fatalf("sandbox fixture did not finish: result=%v stdout=%q stderr=%q cleanup=%v", result, stdout.String(), stderr.String(), cleanupErr)
		}
		result = call(t, e, r, "write_stdin", map[string]any{"session_id": result["session_id"], "yield_time_ms": 1000})
	}
}

func completedSandboxCommand(t *testing.T, e *Engine, r Request, command string) map[string]any {
	t.Helper()
	// A short yield deliberately exercises the real asynchronous result contract.
	return completedSandboxResult(t, e, r, call(t, e, r, "exec_command", map[string]any{"cmd": command, "yield_time_ms": 1}))
}

func TestRealSandboxCommandAndPatchWorker(t *testing.T) {
	e, r := sandboxFixture(t)
	// Production workspaces live below the protected Control root. The workspace
	// exception must work without exposing sibling state beneath that same root.
	e.options.ProtectedPaths = []string{filepath.Dir(r.CWD)}
	secret := filepath.Join(filepath.Dir(r.CWD), "control-secret")
	if err := os.WriteFile(secret, []byte("fake-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	probe := completedSandboxCommand(t, e, r, "if cat "+shellQuote(secret)+"; then exit 9; fi; printf confined")
	if probe["exit_code"] != 0 || probe["stdout"] != "confined" {
		t.Fatal("protected root exposure", probe)
	}
	result := completedSandboxCommand(t, e, r, "printf sandbox; printf ok > proof")
	if result["stdout"] != "sandbox" || result["exit_code"] != 0 {
		t.Fatal(result)
	}
	result = call(t, e, r, "apply_patch", map[string]any{"patch": "*** Begin Patch\n*** Add File: patched\n+hello\n*** End Patch"})
	if !strings.Contains(result["output"].(string), "patched") {
		t.Fatal(result)
	}
	if data, err := os.ReadFile(filepath.Join(r.CWD, "patched")); err != nil || string(data) != "hello\n" {
		t.Fatal(string(data), err)
	}
	if err := os.Mkdir(filepath.Join(r.CWD, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.CWD, ".git/config"), []byte("fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	r.Operation = "apply_patch"
	r.Args = map[string]any{"patch": "*** Begin Patch\n*** Update File: .git/config\n@@\n-fixture\n+modified\n*** End Patch"}
	if _, err := e.Execute(context.Background(), r, nil); err == nil {
		t.Fatal("patch worker bypassed protected Git config")
	}
	if data, err := os.ReadFile(filepath.Join(r.CWD, ".git/config")); err != nil || string(data) != "fixture\n" {
		t.Fatal("protected file changed", string(data), err)
	}
}

func TestRealSandboxExternalPatchRequiresWritePermission(t *testing.T) {
	e, r := sandboxFixture(t)
	outside := t.TempDir()
	path := filepath.Join(outside, "profile.json")
	sibling := filepath.Join(outside, "private.sqlite")
	for _, name := range []string{path, sibling} {
		if err := os.WriteFile(name, []byte("old\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	r.Operation = "apply_patch"
	r.Args = map[string]any{"patch": "*** Begin Patch\n*** Update File: " + path + "\n@@\n-old\n+new\n*** End Patch"}
	if _, err := e.Execute(t.Context(), r, nil); err == nil {
		t.Fatal("external file was writable without permission")
	}
	e.options.SandboxRead = []string{path}
	if _, err := e.Execute(t.Context(), r, nil); err == nil {
		t.Fatal("read permission granted writes")
	}
	if data, _ := os.ReadFile(path); string(data) != "old\n" {
		t.Fatal("denied patch changed the host file")
	}
	e.options.SandboxWrite = []string{path}
	beforeExact, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Execute(t.Context(), r, nil); err != nil {
		t.Fatal("exact file grant did not allow external patch", err)
	}
	if data, _ := os.ReadFile(path); string(data) != "new\n" {
		t.Fatal("patch succeeded without changing the actual host file")
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("exact file patch changed permissions", info, err)
	}
	if after, err := os.Stat(path); err != nil || !os.SameFile(beforeExact, after) {
		t.Fatal("exact file grant did not use in-place update", err)
	}
	r.Args["patch"] = "*** Begin Patch\n*** Update File: " + sibling + "\n@@\n-old\n+changed\n*** End Patch"
	if _, err := e.Execute(t.Context(), r, nil); err == nil {
		t.Fatal("one file grant exposed its sibling")
	}
	if data, _ := os.ReadFile(sibling); string(data) != "old\n" {
		t.Fatal("sibling host file changed")
	}
	if runtime.GOOS == "linux" {
		// A read-only ancestor can shadow the SDK's writable file mount.
		e.options.SandboxRead = []string{outside}
		r.Args["patch"] = "*** Begin Patch\n*** Update File: " + path + "\n@@\n-new\n+changed\n*** End Patch"
		if _, err := e.Execute(t.Context(), r, nil); err == nil {
			t.Fatal("patch bypassed read-only target mount")
		}
		if data, _ := os.ReadFile(path); string(data) != "new\n" {
			t.Fatal("read-only denial changed host file")
		}
	}
	// Directory grants must replace atomically, including redundant file binds.
	e.options.SandboxRead = []string{outside}
	old := "new"
	for _, grants := range [][]string{{outside}, {path, outside}, {outside, path}} {
		before, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		e.options.SandboxWrite = grants
		next := old + "!"
		r.Args["patch"] = "*** Begin Patch\n*** Update File: " + path + "\n@@\n-" + old + "\n+" + next + "\n*** End Patch"
		if _, err := e.Execute(t.Context(), r, nil); err != nil {
			t.Fatal("directory grant patch failed", grants, err)
		}
		after, err := os.Stat(path)
		if err != nil || os.SameFile(before, after) {
			t.Fatal("directory grant did not replace atomically", grants, err)
		}
		if data, err := os.ReadFile(path); err != nil || string(data) != next+"\n" {
			t.Fatal("directory patch did not update host file", string(data), err)
		}
		old = next
	}
	r.Args["patch"] = "*** Begin Patch\n*** Update File: " + sibling + "\n@@\n-old\n+changed\n*** End Patch"
	r.Mode = "host"
	if _, err := e.Execute(t.Context(), r, nil); err != nil {
		t.Fatal("authorized host mode could not edit the external file", err)
	}
}
func TestRealSandboxWorkspacePatchWithExactGrantIsAtomic(t *testing.T) {
	e, r := sandboxFixture(t)
	path := filepath.Join(r.CWD, "workspace-file")
	if err := os.WriteFile(path, []byte("old\n"), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	e.options.SandboxWrite = []string{path}
	r.Operation = "apply_patch"
	r.Args = map[string]any{"patch": "*** Begin Patch\n*** Update File: workspace-file\n@@\n-old\n+new\n*** End Patch"}
	if _, err := e.Execute(t.Context(), r, nil); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil || os.SameFile(before, after) {
		t.Fatal("workspace write did not retain atomic replacement", err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "new\n" {
		t.Fatal("workspace patch did not change actual contents", string(data), err)
	}
}

func TestRealSandboxTTYAndInput(t *testing.T) {
	e, r := sandboxFixture(t)
	result := call(t, e, r, "exec_command", map[string]any{"cmd": "test -t 0 && echo terminal; read -r value; stty size; echo received:$value", "tty": true, "yield_time_ms": 20})
	id, ok := result["session_id"].(string)
	if !ok {
		t.Fatal(result)
	}
	result = call(t, e, r, "write_stdin", map[string]any{"session_id": id, "rows": 40, "cols": 100, "chars": "hello\n", "yield_time_ms": 1000})
	result = completedSandboxResult(t, e, r, result)
	output := result["stdout"].(string)
	if !strings.Contains(output, "40 100") || !strings.Contains(output, "received:hello") {
		t.Fatal(output)
	}
}
func TestRealSandboxDetachedGrandchildCleanup(t *testing.T) {
	e, r := sandboxFixture(t)
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	code := `import os,time
if os.fork():
 for i in range(500):
  if os.path.exists('detached-pid'): os._exit(0)
  time.sleep(.01)
 os._exit(1)
os.setsid()
if os.fork(): os._exit(0)
open('detached-heartbeat','w').write('0')
open('detached-pid','w').write(str(os.getpid()))
for i in range(1,501):
 time.sleep(.01)
 open('heartbeat-next','w').write(str(i))
 os.replace('heartbeat-next','detached-heartbeat')
`
	encoded, _ := json.Marshal(code)
	script := "import json; exec(json.loads(" + strconv.Quote(string(encoded)) + "))"
	command := shellQuote(python) + " -c " + shellQuote(script)
	result := completedSandboxCommand(t, e, r, command)
	if result["exit_code"] != 0 {
		t.Fatal(result)
	}
	if runtime.GOOS == "linux" {
		// The recorded PID belongs to bwrap's private PID namespace. Never
		// signal that number on the host, where it may name an unrelated process.
		path := filepath.Join(r.CWD, "detached-heartbeat")
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		deadline := time.NewTimer(500 * time.Millisecond)
		defer deadline.Stop()
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-deadline.C:
				return
			case <-ticker.C:
				after, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(before, after) {
					t.Fatalf("detached grandchild wrote after cleanup: %q -> %q: %v", before, after, err)
				}
			}
		}
	}
	data, err := os.ReadFile(filepath.Join(r.CWD, "detached-pid"))
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(string(data))
	if err != nil {
		t.Fatal(err)
	}
	if err = syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("detached grandchild survived sandbox cleanup: %v", err)
	}
}
func TestRealSandboxCancellationKeepsFiles(t *testing.T) {
	e, r := sandboxFixture(t)
	r.Operation = "exec_command"
	r.Args = map[string]any{"cmd": "echo retained > retained; echo ready; sleep 90", "yield_time_ms": 10}
	result, err := e.Execute(context.Background(), r, nil)
	if err != nil {
		t.Fatal(err)
	}
	id, ok := result["session_id"].(string)
	if !ok {
		t.Fatal(result)
	}
	for !strings.Contains(result["stdout"].(string), "ready") {
		result = call(t, e, r, "write_stdin", map[string]any{"session_id": id, "yield_time_ms": 100})
	}
	result = call(t, e, r, "write_stdin", map[string]any{"session_id": id, "terminate": true, "yield_time_ms": 1000})
	if result["session_id"] != nil {
		t.Fatal(result)
	}
	if _, err = os.Stat(filepath.Join(r.CWD, "retained")); err != nil {
		t.Fatal("cleanup erased workspace", err)
	}
}
func shellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }

func TestRealSandboxPersistentPlugin(t *testing.T) {
	e, r := sandboxFixture(t)
	bundle := counterBundle(t)
	first := call(t, e, r, "plugin_call", pluginArgs(t, bundle, map[string]any{}))
	id := first["session_id"].(string)
	next := call(t, e, r, "plugin_call", pluginArgs(t, bundle, map[string]any{"session_id": id}))
	raw, _ := json.Marshal(next)
	if !strings.Contains(string(raw), `"text":"2"`) {
		t.Fatal(string(raw))
	}
	call(t, e, r, "plugin_call", pluginArgs(t, bundle, map[string]any{"session_id": id, "reset": true}))
	if err := e.Drain(); err != nil {
		t.Fatal(err)
	}
}

// A platform may reject Chromium's nested native sandbox. Qualification accepts
// that explicit failure, but never host fallback or unconfirmed process cleanup.
func TestRealSandboxChromiumQualification(t *testing.T) {
	e, r := sandboxFixture(t)
	source := os.Getenv("POT_SANDBOX_BROWSER_SOURCE")
	if source == "" {
		source = "../../plugins/computer-use/browser.mjs"
	}
	code, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(r.CWD, "browser.mjs"), code, 0600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<title>Sandbox browser proof</title><button>Proof</button>"))
	}))
	defer server.Close()
	url, _ := json.Marshal(server.URL)
	script := `import {operation,close} from './browser.mjs'; try { const result=await operation({action:'open',url:` + string(url) + `,screenshot:true}); console.log('BROWSER_OK:'+result.content[0].text); await close(); } catch(e) { console.error('BROWSER_UNSUPPORTED:'+e.message); process.exitCode=42; }`
	result := completedSandboxCommand(t, e, r, "node --input-type=module -e "+shellQuote(script))
	stdout, stderr := result["stdout"].(string), result["stderr"].(string)
	if result["exit_code"] == 42 && strings.Contains(stderr, "BROWSER_UNSUPPORTED:") {
		t.Log("Chromium rejected protected execution; no host fallback: " + stderr)
	} else if result["exit_code"] != 0 || !strings.Contains(stdout, "Sandbox browser proof") || !strings.Contains(stdout, "Proof") {
		t.Fatal("Chromium/CDP qualification failed", result, stdout, stderr)
	} else {
		t.Log("Chromium native sandbox, CDP pipe, DOM and screenshot succeeded")
	}
	if err = e.Drain(); err != nil {
		t.Fatal("browser descendant cleanup unconfirmed", err)
	}
}

func TestRealSandboxCommandPreservesToolchainPATH(t *testing.T) {
	e, r := sandboxFixture(t)
	// The sandbox helper uses configured Node; ordinary commands retain PATH.
	conflictingBin := filepath.Join(r.CWD, "other-bin")
	if err := os.Mkdir(conflictingBin, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"node", "npm", "npx"} {
		if err := os.WriteFile(filepath.Join(conflictingBin, name), []byte("#!/bin/sh\necho project-"+name+"\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	paths := []string{conflictingBin}
	for _, path := range filepath.SplitList(os.Getenv("PATH")) {
		if path != filepath.Dir(e.options.Node) {
			paths = append(paths, path)
		}
	}
	// Keep Linux SRT dependencies (rg, socat, bwrap) available to the helper.
	t.Setenv("PATH", strings.Join(paths, string(os.PathListSeparator)))
	result := completedSandboxCommand(t, e, r, "node --version && npm --version && npx --version")
	stdout := result["stdout"].(string)
	if result["exit_code"] != 0 || stdout != "project-node\nproject-npm\nproject-npx\n" {
		t.Fatal(result, stdout)
	}
}

func TestRealSandboxSharedToolBinDoesNotCoverWorkspace(t *testing.T) {
	e, r := sandboxFixture(t)
	shared := filepath.Dir(r.CWD)
	bin := filepath.Join(shared, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte("#!/bin/sh\nprintf tool"), 0700); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(shared, "tool-neighbor-secret")
	if err := os.WriteFile(secret, []byte("private-neighbor"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	result := completedSandboxCommand(t, e, r, "if cat "+shellQuote(secret)+"; then exit 77; fi; printf written > tool-proof; gh")
	stdout, stderr := result["stdout"].(string), result["stderr"].(string)
	if result["exit_code"] != 0 || stdout != "tool" {
		t.Fatal("shared tool root exposed siblings or covered workspace writes", result, stdout, stderr)
	}
	if data, err := os.ReadFile(filepath.Join(r.CWD, "tool-proof")); err != nil || string(data) != "written" {
		t.Fatal(string(data), err)
	}
}

func TestRealSandboxDirectCommand(t *testing.T) {
	e, r := sandboxFixture(t)
	result, err := e.RunCommand(context.Background(), r, CommandInput{Argv: []string{"/bin/sh", "-c", `read context; printf '%s' "$context"`}, Input: "{\"occurrence_id\":\"fixture\"}\n"})
	if err != nil || result["exit_code"] != 0 || result["stdout"] != `{"occurrence_id":"fixture"}` {
		t.Fatal(result, err)
	}
}
