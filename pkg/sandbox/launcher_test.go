package sandbox

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jjyr/pot/internal/testutil"
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

func TestProtectedRootAliasesKeepNestedWorkspaceWritable(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	workspace := filepath.Join(root, "state", "workspace")
	outside := filepath.Join(root, "other-state")
	sdk := filepath.Join(root, "sdk")
	for _, path := range []string{workspace, outside, sdk} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	alias := filepath.Join(root, "state-alias")
	outsideAlias := filepath.Join(root, "other-alias")
	for link, target := range map[string]string{alias: filepath.Dir(workspace), outsideAlias: outside} {
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}
	// Inspect the actual request sent to the SDK adapter without executing a workload.
	helper := `import {createInterface} from 'node:readline';
const lines=createInterface({input:process.stdin});
const first=await lines[Symbol.asyncIterator]().next();
process.stdout.write(JSON.stringify({argv:['/bin/sh','-c',first.value]})+'\n');
for await(const line of lines){if(line==='close')break;}
lines.close();`
	if err := os.WriteFile(filepath.Join(sdk, "launcher.mjs"), []byte(helper), 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("/bin/sh", "-c", "true")
	command.Dir = workspace
	finish, err := (Launcher{Node: node, Directory: sdk, ProtectedPaths: []string{alias, outsideAlias}}).Prepare(context.Background(), command, workspace, nil, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := finish(); err != nil {
			t.Error(err)
		}
	}()
	var request launch
	if err := json.Unmarshal([]byte(command.Args[2]), &request); err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range request.DenyWrite {
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			t.Fatal(err)
		}
		if within(canonical, resolved) {
			t.Fatalf("protected alias denies its nested workspace: %s", path)
		}
	}
	for _, path := range []string{filepath.Dir(workspace), outside} {
		canonical, err := filepath.EvalSymlinks(path)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, denied := range request.DenyRead {
			found = found || denied == canonical
		}
		if !found {
			t.Fatalf("canonical authority root lost read protection: %s", canonical)
		}
	}
	canonicalOutside, err := filepath.EvalSymlinks(outside)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range request.DenyWrite {
		if path == canonicalOutside {
			return
		}
	}
	t.Fatal("unrelated authority root lost write protection")
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
	readAlias := filepath.Join(root, "read-alias")
	if err := os.Symlink("/etc/passwd", readAlias); err != nil {
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
	socket := filepath.Join(workspace, "docker.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	agentSocket := filepath.Join(workspace, "agent.sock")
	agent, err := net.Listen("unix", agentSocket)
	if err != nil {
		t.Fatal(err)
	}
	defer agent.Close()
	t.Setenv("SSH_AUTH_SOCK", agentSocket)
	t.Setenv("DOCKER_HOST", "unix://"+socket)
	alias := filepath.Join(workspace, "docker-alias.sock")
	if err := os.Symlink(socket, alias); err != nil {
		t.Fatal(err)
	}
	sockets := map[string]string{"denyDockerSocket": socket, "denySSHAgentSocket": agentSocket, "denySocketAlias": alias}
	if runtime.GOOS == "linux" {
		address := "@" + filepath.Base(root)
		abstract, err := net.Listen("unix", address)
		if err != nil {
			t.Fatal(err)
		}
		defer abstract.Close()
		sockets["denyAbstractSocket"] = address
	}
	// These are real, reachable services. A missing or hidden endpoint must not
	// be mistaken for enforcement of the socket boundary.
	for name, address := range sockets {
		connection, err := net.DialTimeout("unix", address, time.Second)
		if err != nil {
			t.Fatalf("host socket control %s: %v", name, err)
		}
		connection.Close()
		if strings.HasPrefix(address, "@") {
			sockets[name] = "\x00" + address[1:]
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("network-open")) }))
	defer server.Close()
	// Exercise system DNS rather than connecting only to a numeric address.
	url := strings.Replace(server.URL, "127.0.0.1", "localhost", 1)
	systemFiles := map[string]string{}
	dnsServers := ""
	{
		probe := exec.Command(node, "-p", "JSON.stringify(require('node:dns').getServers())")
		probe.Env = Environment()
		output, err := probe.Output()
		if err != nil {
			t.Fatal(err)
		}
		dnsServers = strings.TrimSpace(string(output))
		caFiles := 0
		for i, path := range []string{"/etc/hosts", "/etc/resolv.conf", "/etc/ssl/cert.pem", "/etc/ssl/certs/ca-certificates.crt", "/etc/ssl/ca-bundle.pem", "/etc/pki/tls/certs/ca-bundle.crt", "/etc/pki/tls/cacert.pem", "/etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem"} {
			data, err := os.ReadFile(path)
			if i >= 2 && os.IsNotExist(err) {
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			if i >= 2 {
				caFiles++
			}
			digest := sha256.Sum256(data)
			systemFiles[path] = fmt.Sprintf("%x", digest)
		}
		if caFiles == 0 {
			t.Fatal("no system CA bundle found")
		}
	}
	values, _ := json.Marshal(map[string]any{"secret": filepath.Join(root, "secret"), "readAlias": readAlias, "outside": filepath.Join(root, "outside"), "sockets": sockets, "url": url, "systemFiles": systemFiles, "dnsServers": dnsServers})
	code := `const fs=require('node:fs'),net=require('node:net'),http=require('node:http');const v=` + string(values) + `;const checks={};
for(const [key,fn] of Object.entries({denyRead:()=>fs.readFileSync(v.secret),denySymlink:()=>fs.readFileSync('escape'),denyReadAlias:()=>fs.readFileSync(v.readAlias),denySystemFile:()=>fs.readFileSync('/etc/passwd'),denyTLSConfig:()=>fs.readFileSync('/etc/ssl/openssl.cnf'),denyWrite:()=>fs.writeFileSync(v.outside,'x'),denyGitConfig:()=>fs.writeFileSync('.git/config','x')})){try{fn();checks[key]=false}catch(e){checks[key]=['EPERM','EACCES','EROFS','ENOENT'].includes(e.code)}}
checks.systemFiles=Object.entries(v.systemFiles).every(([path,digest])=>{try{return require('node:crypto').createHash('sha256').update(fs.readFileSync(path)).digest('hex')===digest}catch{return false}});
checks.dnsConfiguration=!v.dnsServers||JSON.stringify(require('node:dns').getServers())===v.dnsServers;
checks.socketVisible=Object.values(v.sockets).filter(path=>!path.startsWith('\0')).every(path=>fs.statSync(path).isSocket());
fs.writeFileSync('output','ok');checks.workspace=fs.readFileSync('output','utf8')==='ok';
checks.noProxy=!process.env.HTTP_PROXY&&!process.env.HTTPS_PROXY&&!process.env.ALL_PROXY;
checks.noAuthorityEnv=!process.env.SSH_AUTH_SOCK&&!process.env.DOCKER_HOST;
Promise.all([...Object.entries(v.sockets).map(([name,path])=>new Promise(resolve=>{const s=net.connect(path);s.once('connect',()=>{checks[name]=false;s.destroy();resolve()});s.once('error',e=>{checks[name]=['EPERM','EACCES'].includes(e.code);resolve()})})),new Promise((resolve,reject)=>{http.get(v.url,r=>{let data='';r.on('data',c=>data+=c);r.on('end',()=>{checks.network=data==='network-open';resolve()})}).on('error',reject)})]).then(()=>console.log(JSON.stringify(checks))).catch(e=>{console.error(e);process.exitCode=1});`
	command := exec.Command(node, "-e", code)
	command.Dir = workspace
	command.Env = Environment()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	finish, err := (Launcher{Node: node, Directory: sdk}).Prepare(ctx, command, workspace, []string{readAlias}, nil, false)
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
	if len(checks) != 14+len(sockets) {
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

func TestRealMacOSCertificateTrust(t *testing.T) {
	// The system trust service must reject a local untrusted certificate with
	// UnknownAuthorityError, rather than fail to access the service itself.
	if path := os.Getenv("POT_SANDBOX_TRUST_CERT"); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		cert, err := x509.ParseCertificate(data)
		if err != nil {
			t.Fatal(err)
		}
		_, err = cert.Verify(x509.VerifyOptions{DNSName: "example.com"})
		var untrusted x509.UnknownAuthorityError
		if !errors.As(err, &untrusted) {
			t.Fatalf("expected untrusted certificate, got %T: %v", err, err)
		}
		return
	}
	if os.Getenv("POT_SANDBOX_INTEGRATION") != "1" || runtime.GOOS != "darwin" {
		t.Skip("macOS sandbox qualification only")
	}
	server := httptest.NewTLSServer(nil)
	defer server.Close()
	cert := server.Certificate()
	_, err := cert.Verify(x509.VerifyOptions{DNSName: "example.com"})
	var untrusted x509.UnknownAuthorityError
	if !errors.As(err, &untrusted) {
		t.Fatalf("host fixture: expected untrusted certificate, got %T: %v", err, err)
	}
	workspace := t.TempDir()
	certPath := filepath.Join(workspace, "untrusted.der")
	if err := os.WriteFile(certPath, cert.Raw, 0600); err != nil {
		t.Fatal(err)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	sdk := os.Getenv("POT_SANDBOX_SDK")
	if sdk == "" {
		sdk, err = filepath.Abs("../../sandbox")
		if err != nil {
			t.Fatal(err)
		}
	}
	worker, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), testutil.Timeout(t, 20*time.Second))
	defer cancel()
	command := exec.CommandContext(ctx, worker, "-test.run=^TestRealMacOSCertificateTrust$")
	command.Dir = workspace
	command.Env = append(Environment(), "POT_SANDBOX_TRUST_CERT="+certPath)
	finish, err := (Launcher{Node: node, Directory: sdk}).Prepare(ctx, command, workspace, nil, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	output, runErr := command.CombinedOutput()
	if cleanup := finish(); runErr != nil || cleanup != nil {
		t.Fatalf("sandbox: %v cleanup: %v\n%s", runErr, cleanup, output)
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
