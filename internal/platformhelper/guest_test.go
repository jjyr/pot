package platformhelper

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGuestDoesNotRedirectCredentials(t *testing.T) {
	seen := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen = true }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	if _, err := (GuestClient{BaseURL: source.URL, Token: "private"}).Call(context.Background(), "execute", map[string]any{}); err == nil || seen {
		t.Fatalf("followed redirect with credentials: %v", err)
	}
}

// Imports the actual retained macOS Python broker without invoking main/root/VM
// setup, and serves its real Handler against a private temporary workspace.
func TestRetainedPythonHealthAndPrepareProtocol(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	source, err := filepath.Abs("../../crates/sandbox-images/macos/xcode")
	if err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	workspace, err = filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatal(err)
	}
	const script = `import importlib.util, pathlib, sys
sys.path.insert(0,sys.argv[1])
spec=importlib.util.spec_from_file_location("retained_executor",pathlib.Path(sys.argv[1])/"executor.py")
module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module)
module.TOKEN="isolated-probe-token"
module.WORK=pathlib.Path(sys.argv[2]);module.PREFIX=str(module.WORK)
server=module.ThreadingHTTPServer(("127.0.0.1",0),module.Handler)
print("http://127.0.0.1:"+str(server.server_port),flush=True)
server.serve_forever()
`
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "-B", "-c", script, source, workspace)
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	line, err := bufio.NewReader(output).ReadString('\n')
	if err != nil {
		t.Fatalf("Python broker setup: %v %s", err, stderr.String())
	}
	client := GuestClient{BaseURL: strings.TrimSpace(line), Token: "isolated-probe-token", Timeout: 2 * time.Second}
	health, err := client.Call(ctx, "health", nil)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if json.Unmarshal(health, &value) != nil || value["ready"] != true || value["protocol"] != "pot-macos-executor-v3" {
		t.Fatalf("bad health %s", health)
	}
	request := map[string]any{"cwd": workspace, "role": "main", "tool": "exec_command", "args": map[string]any{"cmd": "unused"}, "policy": map[string]any{"executable": "/bin/bash", "allow_login_shell": true, "default_timeout_ms": 120000}}
	prepared, err := client.Call(ctx, "prepare", request)
	if err != nil {
		t.Fatal(err)
	}
	if json.Unmarshal(prepared, &value) != nil {
		t.Fatal("bad prepare JSON")
	}
	args := value["args"].(map[string]any)
	if args["workdir"] != workspace || args["shell"] != "/bin/bash" || args["login"] != true || args["tty"] != false || args["timeout_ms"] != float64(120000) {
		t.Fatalf("lost retained normalization: %s", prepared)
	}
	client.Token = "wrong-token"
	if _, err := client.Call(ctx, "health", nil); err == nil {
		t.Fatal("broker accepted wrong token")
	}
}
