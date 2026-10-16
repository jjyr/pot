package sandboxprovider

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/core"
	"github.com/jjyr/pot/internal/sandbox"
)

// Explicitly opt in with a newly allocated private qualification root. The test
// never adopts an existing Runtime, VM, container or Podman storage directory.
func TestRealLinuxSandboxQualification(t *testing.T) {
	root := os.Getenv("POT_GO_SANDBOX_QUALIFY_ROOT")
	if root == "" {
		t.Skip("requires isolated real shared Podman machine or rootless Linux Podman")
	}
	if !filepath.IsAbs(root) {
		t.Fatal("qualification root must be absolute")
	}
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatalf("qualification root must be fresh: %v", err)
	}
	if seed := os.Getenv("POT_GO_PODMAN_MACHINE_IMAGE_CACHE"); seed != "" {
		source, err := os.Open(seed)
		if err != nil {
			t.Fatal(err)
		}
		info, err := source.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() != podmanAppleHVImage.Size {
			source.Close()
			t.Fatal("machine image seed must be the pinned regular archive")
		}
		cache := filepath.Join(root, "podman-machine/cache")
		if err = os.MkdirAll(cache, 0700); err != nil {
			source.Close()
			t.Fatal(err)
		}
		destination, err := os.OpenFile(filepath.Join(cache, "machine-os-"+podmanAppleHVImage.SHA256+".raw.zst"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			source.Close()
			t.Fatal(err)
		}
		_, err = io.Copy(destination, source)
		source.Close()
		closeErr := destination.Close()
		if err != nil || closeErr != nil {
			t.Fatal(err, closeErr)
		}
		// Production prepare still hashes every byte before using this seed.
	}

	user, builtin := filepath.Join(root, "user-skills"), filepath.Join(root, "builtin-skills")
	for _, dir := range []string{user, builtin} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	userBundle := filepath.Join(user, "qualification-user")
	if err := os.Mkdir(userBundle, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(userBundle, "SKILL.md"), []byte("---\nname: qualification-user\ndescription: isolated test\n---\nQualification only.\n"), 0644); err != nil {
		t.Fatal(err)
	}
	provider := NewPodman(PodmanConfig{RuntimeRoot: root, Binary: os.Getenv("POT_GO_SANDBOX_BINARY"), MachineName: os.Getenv("POT_GO_PODMAN_MACHINE_NAME"), UserSkills: user, BuiltinSkills: builtin})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	manager, err := sandbox.Open(ctx, sandbox.Config{Root: root, RuntimeID: "go-qualification", Providers: []sandbox.Provider{provider}})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := manager.Shutdown(ctx); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	}()
	definition, err := manager.EnsureShared(ctx)
	if err != nil {
		t.Fatal(err)
	}
	{
		var prepared sandbox.PreparedResources
		data, err := os.ReadFile(os.Getenv("POT_GO_SANDBOX_PREPARED"))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, &prepared); err != nil {
			t.Fatal(err)
		}
		if err := manager.BindResources(ctx, definition.ID, prepared); err != nil {
			t.Fatal(err)
		}
	}
	policy := core.DefaultShellSettings()
	policy.AllowLoginShell, policy.ShellSnapshot = false, false
	grant := sandbox.AccessGrant{Target: core.SandboxTarget("go-qualification", definition.ID), Owner: "qualification", Turn: "first", ThreadGeneration: 1, Policy: policy}
	call := func(id, name string, args map[string]any) map[string]any {
		t.Helper()
		body, err := json.Marshal(args)
		if err != nil {
			t.Fatal(err)
		}
		prepared, err := manager.PrepareOperation(ctx, grant, name, body)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := manager.Execute(ctx, grant, id, name, prepared, nil)
		if err != nil {
			t.Fatal(err)
		}
		var result map[string]any
		if err := json.Unmarshal(raw, &result); err != nil {
			t.Fatal(err)
		}
		if result["isError"] == true {
			t.Fatalf("%s: %s", name, raw)
		}
		if code, ok := result["exit_code"].(float64); ok && code != 0 {
			t.Fatalf("%s exited %v: %s", name, code, raw)
		}
		return result
	}
	command := map[string]any{"cmd": "printf x >> effects; printf 'ready uid='; id -u; test ! -r /run/pot-executor/operations.sqlite; sha256sum /opt/pot/pot-tool-executor", "timeout_ms": 10000}
	first := call("exact-once", "exec_command", command)
	second := call("exact-once", "exec_command", command)
	a, _ := json.Marshal(first)
	b, _ := json.Marshal(second)
	if string(a) != string(b) {
		t.Fatal("saved duplicate result changed")
	}
	if hash := os.Getenv("POT_GO_SANDBOX_EXECUTOR_SHA256"); hash == "" || !strings.Contains(string(a), hash) {
		t.Fatalf("guest did not execute the selected Go binary: %s", a)
	}
	if !strings.Contains(string(a), "uid=1000") {
		t.Fatalf("guest tools did not drop privileges: %s", a)
	}
	workspace, err := manager.Workspace(ctx, definition.ID)
	if err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(workspace, "effects")); err != nil || string(data) != "x" {
		t.Fatalf("effects=%q %v", data, err)
	}
	call("patch", "apply_patch", map[string]any{"patch": "*** Begin Patch\n*** Add File: patched\n+go migration\n*** End Patch"})
	if data, err := os.ReadFile(filepath.Join(workspace, "patched")); err != nil || string(data) != "go migration\n" {
		t.Fatalf("patch=%q %v", data, err)
	}
	session := call("pty", "exec_command", map[string]any{"cmd": "read -r x; printf 'received:%s' \"$x\"", "tty": true, "yield_time_ms": 50})["session_id"]
	if session == nil {
		t.Fatal("PTY session missing")
	}
	result := call("stdin", "write_stdin", map[string]any{"session_id": session, "chars": "hello\n", "yield_time_ms": 1000})
	if raw, _ := json.Marshal(result); !strings.Contains(string(raw), "received:hello") {
		t.Fatalf("PTY input: %s", raw)
	}
	call("background", "exec_command", map[string]any{"cmd": "sleep 120 & printf '%s' $! > child-pid; wait", "yield_time_ms": 50})
	if err := manager.Cancel(ctx, grant.Owner); err != nil {
		t.Fatal(err)
	}
	grant.Owner, grant.Turn = "second-owner", "second"
	result = call("reaped", "exec_command", map[string]any{"cmd": "pid=$(cat child-pid); state=$(awk '{print $3}' /proc/\"$pid\"/stat 2>/dev/null || true); case \"$state\" in Z|\"\") printf contained;; *) echo leaked:$state; exit 1;; esac", "timeout_ms": 10000})
	if raw, _ := json.Marshal(result); !strings.Contains(string(raw), "contained") {
		t.Fatalf("scope cleanup: %s", raw)
	}
	if err := manager.Finish(ctx, grant.Owner); err != nil {
		t.Fatal(err)
	}
	grant.Owner, grant.WorkspaceOwner, grant.Worker = "task-owner", "qualification-task", true
	result = call("readonly-skills", "exec_command", map[string]any{"cmd": "test -r \"$HOME/.pot/agent/skills/qualification-user/SKILL.md\"; if printf forbidden > \"$HOME/.pot/agent/skills/qualification-user/task-write\" 2>/dev/null; then echo writable; exit 1; fi; printf readonly", "timeout_ms": 10000})
	if raw, _ := json.Marshal(result); !strings.Contains(string(raw), "readonly") {
		t.Fatalf("task skill view: %s", raw)
	}
	if err := manager.Finish(ctx, grant.Owner); err != nil {
		t.Fatal(err)
	}
	grant.Owner, grant.WorkspaceOwner, grant.Worker = "second-owner", "", false
	if _, err := manager.Lifecycle(ctx, definition.ID, "stop"); err != nil {
		t.Fatal(err)
	}
	grant.Turn = "restart"
	result = call("persist", "exec_command", map[string]any{"cmd": "cat patched; cat effects", "timeout_ms": 10000})
	if raw, _ := json.Marshal(result); !strings.Contains(string(raw), "go migration") {
		t.Fatalf("restart: %s", raw)
	}
	if os.Getenv("POT_GO_SANDBOX_QUALIFY_DESKTOP") == "1" {
		binding, err := manager.PrepareDesktop(ctx, definition.ID)
		if err != nil {
			t.Fatal(err)
		}
		stream, err := manager.OpenDesktop(ctx, binding)
		if err != nil {
			t.Fatal(err)
		}
		defer stream.Close()
		banner := make([]byte, 12)
		if _, err := io.ReadFull(stream, banner); err != nil || string(banner) != "RFB 003.008\n" {
			t.Fatalf("actual desktop banner %q %v", banner, err)
		}
		if _, err := stream.Write(banner); err != nil {
			t.Fatal(err)
		}
		count := make([]byte, 1)
		if _, err := io.ReadFull(stream, count); err != nil || count[0] == 0 {
			t.Fatalf("desktop security negotiation %v %v", count, err)
		}
		types := make([]byte, int(count[0]))
		if _, err := io.ReadFull(stream, types); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, kind := range types {
			found = found || kind == 1
		}
		if !found {
			t.Fatal("private desktop lacks None security")
		}
		if _, err := stream.Write([]byte{1}); err != nil {
			t.Fatal(err)
		}
		security := make([]byte, 4)
		if _, err := io.ReadFull(stream, security); err != nil || binary.BigEndian.Uint32(security) != 0 {
			t.Fatalf("desktop security result %v %v", security, err)
		}
		if _, err := stream.Write([]byte{1}); err != nil {
			t.Fatal(err)
		}
		init := make([]byte, 24)
		if _, err := io.ReadFull(stream, init); err != nil {
			t.Fatal(err)
		}
		if binary.BigEndian.Uint16(init[:2]) == 0 || binary.BigEndian.Uint16(init[2:4]) == 0 {
			t.Fatal("desktop framebuffer missing")
		}
		nameSize := binary.BigEndian.Uint32(init[20:24])
		if nameSize > 65536 {
			t.Fatal("desktop name exceeds bound")
		}
		name := make([]byte, int(nameSize))
		if _, err := io.ReadFull(stream, name); err != nil {
			t.Fatal(err)
		}
		t.Logf("actual desktop RFB 3.8 framebuffer %dx%d", binary.BigEndian.Uint16(init[:2]), binary.BigEndian.Uint16(init[2:4]))
	}
	t.Log(fmt.Sprintf("qualified %s with exact receipts, Go guest binary, PTY, patch, scope containment, readonly Task skills and restart persistence", definition.Provider))
}
