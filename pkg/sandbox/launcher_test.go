package sandbox

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestUnavailableNeverReturnsUnconfinedCommand(t *testing.T) {
	command := exec.Command("/bin/sh", "-c", "true")
	command.Dir = t.TempDir()
	if _, err := (Launcher{}).Prepare(context.Background(), command, command.Dir, nil, nil, false); err == nil {
		t.Fatal("missing sandbox accepted")
	}
}

// Run explicitly on each execution platform; never silently skip a requested
// real sandbox suite because dependencies or kernel support are missing.
func TestRealFileNetworkAndSocketIsolation(t *testing.T) {
	if os.Getenv("POT_SANDBOX_INTEGRATION") != "1" {
		t.Skip("set POT_SANDBOX_INTEGRATION=1 for real OS sandbox qualification")
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Fatal("unsupported qualification platform")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	sdk, err := filepath.Abs("../../sandbox")
	if configured := os.Getenv("POT_SANDBOX_SDK"); configured != "" {
		sdk = configured
	}
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp("/tmp", "pot-srt-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(root, "work")
	if err = os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "secret"), []byte("FAKE_PRIVATE_VALUE"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(filepath.Join(root, "secret"), filepath.Join(workspace, "escape")); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(filepath.Join(workspace, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(workspace, ".git/config"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	// Socket is deliberately inside the readable workspace: denying its path as
	// a file is not sufficient to pass this test.
	socket := filepath.Join(workspace, "daemon.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("network-open")) }))
	defer server.Close()
	values, _ := json.Marshal(map[string]string{"secret": filepath.Join(root, "secret"), "outside": filepath.Join(root, "outside"), "socket": socket, "url": server.URL})
	code := `const fs=require('node:fs'),net=require('node:net'),http=require('node:http');const v=` + string(values) + `;const checks={};
for(const [key,fn] of Object.entries({denyRead:()=>fs.readFileSync(v.secret),denySymlink:()=>fs.readFileSync('escape'),denyWrite:()=>fs.writeFileSync(v.outside,'x'),denyGitConfig:()=>fs.writeFileSync('.git/config','x')})){try{fn();checks[key]=false}catch(e){checks[key]=['EPERM','EACCES','EROFS','ENOENT'].includes(e.code)}}
fs.writeFileSync('output','ok');checks.workspace=fs.readFileSync('output','utf8')==='ok';
checks.noProxy=!process.env.HTTP_PROXY&&!process.env.HTTPS_PROXY&&!process.env.ALL_PROXY;
Promise.all([new Promise(resolve=>{const s=net.connect(v.socket);s.once('connect',()=>{checks.socket=false;s.destroy();resolve()});s.once('error',()=>{checks.socket=true;resolve()})}),new Promise((resolve,reject)=>{http.get(v.url,r=>{let data='';r.on('data',c=>data+=c);r.on('end',()=>{checks.network=data==='network-open';resolve()})}).on('error',reject)})]).then(()=>console.log(JSON.stringify(checks))).catch(e=>{console.error(e);process.exitCode=1});`
	command := exec.Command(node, "-e", code)
	command.Dir = workspace
	command.Env = Environment()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	finish, err := (Launcher{Node: node, Directory: sdk}).Prepare(ctx, command, workspace, nil, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	output, runErr := command.CombinedOutput()
	cleanup := finish()
	if runErr != nil || cleanup != nil {
		t.Fatalf("sandbox: %v cleanup: %v\n%s", runErr, cleanup, output)
	}
	t.Log(string(output))
	var checks map[string]bool
	if err = json.Unmarshal(output, &checks); err != nil {
		t.Fatal(err)
	}
	if len(checks) != 8 {
		t.Fatal("missing isolation checks", checks)
	}
	// Linux hides unreadable directories behind private tmpfs mounts. A write
	// there may succeed in the namespace; it must never reach the host tree.
	if _, err := os.Stat(filepath.Join(root, "outside")); !os.IsNotExist(err) {
		t.Fatalf("outside host write escaped sandbox: %v", err)
	}
	if runtime.GOOS == "linux" && !checks["denyWrite"] {
		t.Log("outside write landed only in the private hidden-directory overlay")
		checks["denyWrite"] = true
	}
	if data, err := os.ReadFile(filepath.Join(workspace, ".git/config")); err != nil || string(data) != "fixture" {
		t.Fatalf("protected Git config changed: %q %v", data, err)
	}
	for name, passed := range checks {
		if !passed {
			t.Errorf("failed: %s", name)
		}
	}
}

func TestRealMissingSeccompFailsBeforeExecution(t *testing.T) {
	if os.Getenv("POT_SANDBOX_INTEGRATION") != "1" || runtime.GOOS != "linux" {
		t.Skip("Linux sandbox qualification only")
	}
	original := os.Getenv("POT_SANDBOX_SDK")
	if original == "" {
		var err error
		original, err = filepath.Abs("../../sandbox")
		if err != nil {
			t.Fatal(err)
		}
	}
	sdk := t.TempDir()
	module := filepath.Join(sdk, "node_modules/@anthropic-ai/sandbox-runtime")
	if err := os.MkdirAll(module, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"package.json", "dist"} {
		if err := os.Symlink(filepath.Join(original, "node_modules/@anthropic-ai/sandbox-runtime", name), filepath.Join(module, name)); err != nil {
			t.Fatal(err)
		}
	}
	code, err := os.ReadFile(filepath.Join(original, "launcher.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(sdk, "launcher.mjs"), code, 0600); err != nil {
		t.Fatal(err)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	command := exec.Command("/bin/sh", "-c", "touch escaped")
	command.Dir = workspace
	command.Env = Environment()
	if finish, err := (Launcher{Node: node, Directory: sdk}).Prepare(context.Background(), command, workspace, nil, nil, false); err == nil {
		_ = finish()
		t.Fatal("missing seccomp silently accepted")
	} else if !strings.Contains(err.Error(), "apply-seccomp") {
		t.Fatal("test did not reach seccomp dependency check", err)
	}
	if _, err = os.Stat(filepath.Join(workspace, "escaped")); !os.IsNotExist(err) {
		t.Fatal("failed preparation ran workload", err)
	}
}
