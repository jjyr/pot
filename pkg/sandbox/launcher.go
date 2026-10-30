// Package sandbox prepares OS-confined launches using a pinned SRT SDK adapter.
// It does not decide whether an operation may run outside the sandbox.
package sandbox

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jjyr/pot/pkg/processscope"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
)

type Launcher struct {
	Markers         *processscope.Markers
	Node, Directory string
	ProtectedPaths  []string
}

type launch struct {
	CWD       string   `json:"cwd"`
	Argv      []string `json:"argv"`
	Read      []string `json:"read"`
	Write     []string `json:"write"`
	DenyRead  []string `json:"denyRead"`
	DenyWrite []string `json:"denyWrite"`
	TTY       bool     `json:"tty"`
}

func within(path, root string) bool {
	return path == root || strings.HasPrefix(path, root+string(filepath.Separator))
}

// Prepare changes only argv/environment. Caller retains stdio, PTY, process
// ownership and cleanup. Finish must run only after workload cleanup is proven.
func (l Launcher) Prepare(ctx context.Context, command *exec.Cmd, workspace string, read, write []string, tty bool) (finish func() error, err error) {
	if l.Directory == "" || l.Node == "" {
		return nil, errors.New("sandbox unavailable: packaged SRT and Node are required; request host execution explicitly")
	}
	readRoots, err := systemReadPaths(runtime.GOOS)
	if err != nil {
		return nil, err
	}
	workspace, err = filepath.EvalSymlinks(workspace)
	if err != nil {
		return nil, err
	}
	if !filepath.IsAbs(workspace) || workspace == "/" {
		return nil, errors.New("sandbox requires a confined absolute workspace")
	}
	protectedPaths := make([]string, 0, len(l.ProtectedPaths))
	for i, protected := range append(append([]string{}, l.ProtectedPaths...), l.Directory, l.Node) {
		canonical, e := filepath.EvalSymlinks(protected)
		if e != nil {
			return nil, fmt.Errorf("sandbox protected path: %w", e)
		}
		if within(canonical, workspace) {
			return nil, errors.New("sandbox workspace contains trusted runtime assets or state")
		}
		if i < len(l.ProtectedPaths) {
			protectedPaths = append(protectedPaths, canonical)
		}
	}
	cwd, err := filepath.EvalSymlinks(command.Dir)
	if err != nil {
		return nil, err
	}
	if !within(cwd, workspace) {
		return nil, errors.New("working directory is outside sandbox workspace; request host execution")
	}
	private, err := os.MkdirTemp("", "pot-sandbox-")
	if err != nil {
		return nil, err
	}
	private, err = filepath.EvalSymlinks(private)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(private)
		}
	}()
	markerDir, err := os.MkdirTemp("", "pot-scope-marker-")
	if err != nil {
		return nil, err
	}
	markerDir, err = filepath.EvalSymlinks(markerDir)
	if err != nil {
		return nil, err
	}
	markerAllow, markerDeny := filepath.Join(markerDir, "allow"), filepath.Join(markerDir, "deny")
	if err = os.WriteFile(markerAllow, nil, 0600); err != nil {
		_ = os.RemoveAll(markerDir)
		return nil, err
	}
	if err = os.WriteFile(markerDeny, nil, 0600); err != nil {
		_ = os.RemoveAll(markerDir)
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(markerDir)
		}
	}()
	if l.Markers != nil {
		*l.Markers = processscope.Markers{Allow: markerAllow, Deny: markerDeny}
	}
	if err = os.Mkdir(filepath.Join(private, "tmp"), 0700); err != nil {
		return nil, err
	}
	request := launch{CWD: cwd, Argv: command.Args, Read: append([]string{workspace, private}, read...), Write: append([]string{workspace, private}, write...), TTY: tty}
	// The packaged Node distribution supplies node plus npm's relative modules.
	// Plugin child tools can use it on PATH without exposing sibling
	// Runtime state or granting writes to trusted tool assets.
	nodePaths := []string{l.Node}
	if bin := filepath.Dir(l.Node); filepath.Base(bin) == "bin" {
		nodePaths = append(nodePaths, bin, filepath.Join(filepath.Dir(bin), "lib/node_modules"))
	}
	request.Read = append(request.Read, nodePaths...)
	// System installation roots are read-only. Never expose the user's home,
	// sibling temporary directories, Control database, or ambient credentials.
	for _, root := range readRoots {
		if canonical, e := filepath.EvalSymlinks(root); e == nil {
			// Preserve the system-facing name as well as its target. Linux
			// read-deny mounts otherwise hide aliases such as /etc/resolv.conf.
			request.Read = append(request.Read, root)
			if canonical != root {
				request.Read = append(request.Read, canonical)
			}
		}
	}
	for _, tool := range []string{"go", "node", "npm", "python3", "git", "gh", "rg"} {
		if path, e := exec.LookPath(tool); e == nil {
			if actual, e := filepath.EvalSymlinks(path); e == nil {
				root := filepath.Dir(actual)
				// A shared bin directory can sit beside the workspace. Restoring
				// its parent read-only would expose siblings and cover write mounts.
				if filepath.Base(root) == "bin" && !within(workspace, filepath.Dir(root)) {
					root = filepath.Dir(root)
				}
				if root != "/" && root != "/Users" && root != "/home" {
					request.Read = append(request.Read, root)
				}
			}
		}
	}
	if canonical, e := filepath.EvalSymlinks(command.Path); e == nil {
		request.Read = append(request.Read, filepath.Dir(canonical))
		command.Path = canonical
		command.Args[0] = canonical
		request.Argv = command.Args
	}
	request.Read = append(request.Read, markerAllow)
	if runtime.GOOS == "linux" {
		request.Read = append(request.Read, filepath.Join(l.Directory, "node_modules/@anthropic-ai/sandbox-runtime/vendor/seccomp"))
	}
	request.DenyRead = append([]string{"/"}, protectedPaths...)
	request.DenyWrite = append([]string{l.Directory, markerDir}, nodePaths...)
	for _, protected := range protectedPaths {
		if !within(workspace, protected) {
			request.DenyWrite = append(request.DenyWrite, protected)
		}
	}
	// The SDK defaults include convenience writes. Deny the real home explicitly
	// without redirecting toolchain configuration or caches. Only scratch files
	// use a per-launch temporary directory. HOME does not grant filesystem access.
	if home, e := os.UserHomeDir(); e == nil && !within(workspace, home) {
		request.DenyWrite = append(request.DenyWrite, home)
	}
	command.Env = ReplaceEnvironment(command.Env, map[string]string{"TMPDIR": filepath.Join(private, "tmp")})
	helper := exec.Command(l.Node, filepath.Join(l.Directory, "launcher.mjs"))
	helper.Dir = cwd
	helper.Env = append(Environment(), "CLAUDE_CODE_TMPDIR="+filepath.Join(private, "tmp"))
	helper.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	input, e := helper.StdinPipe()
	if e != nil {
		return nil, e
	}
	output, e := helper.StdoutPipe()
	if e != nil {
		input.Close()
		return nil, e
	}
	if err = helper.Start(); err != nil {
		input.Close()
		output.Close()
		return nil, err
	}
	wait := make(chan error, 1)
	go func() { wait <- helper.Wait() }()
	var once sync.Once
	var endErr error
	finish = func() error {
		once.Do(func() {
			_, _ = io.WriteString(input, "close\n")
			input.Close()
			select {
			case endErr = <-wait:
			case <-time.After(5 * time.Second):
				_ = syscall.Kill(-helper.Process.Pid, syscall.SIGKILL)
				endErr = errors.New("sandbox helper cleanup unconfirmed")
			}
			output.Close()
			if endErr == nil {
				endErr = errors.Join(os.RemoveAll(private), os.RemoveAll(markerDir))
			}
		})
		return endErr
	}
	defer func(cleanup func() error) {
		if err != nil {
			_ = cleanup()
		}
	}(finish)
	if err = json.NewEncoder(input).Encode(request); err != nil {
		return nil, err
	}
	type response struct {
		Argv  []string `json:"argv"`
		Error string   `json:"error"`
	}
	ready := make(chan struct {
		value response
		err   error
	}, 1)
	go func() {
		scanner := bufio.NewScanner(output)
		scanner.Buffer(make([]byte, 4096), 8*1024*1024)
		var r response
		var e error
		if scanner.Scan() {
			e = json.Unmarshal(scanner.Bytes(), &r)
		} else {
			e = scanner.Err()
			if e == nil {
				e = errors.New("sandbox helper exited before preparing launch")
			}
		}
		ready <- struct {
			value response
			err   error
		}{r, e}
	}()
	var r response
	select {
	case result := <-ready:
		r, err = result.value, result.err
	case <-ctx.Done():
		err = ctx.Err()
	case <-time.After(30 * time.Second):
		err = errors.New("sandbox preparation timed out")
	}
	if err != nil {
		return nil, err
	}
	if r.Error != "" {
		return nil, fmt.Errorf("sandbox unavailable: %s", r.Error)
	}
	if len(r.Argv) < 2 {
		return nil, errors.New("sandbox returned invalid launch")
	}
	command.Path = r.Argv[0]
	command.Args = r.Argv
	return finish, nil
}

// Environment excludes ambient application secrets and startup-code variables.
func Environment() []string {
	values := map[string]string{"PATH": defaultExecutablePath(runtime.GOOS), "LANG": "C.UTF-8"}
	for _, key := range []string{"HOME", "PATH", "LANG", "LC_ALL", "SSL_CERT_FILE", "SSL_CERT_DIR"} {
		if value := os.Getenv(key); value != "" {
			values[key] = value
		}
	}
	return ReplaceEnvironment(nil, values)
}

func defaultExecutablePath(goos string) string {
	path := "/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
	if goos == "darwin" {
		return "/opt/homebrew/bin:" + path
	}
	return path
}

func ReplaceEnvironment(base []string, values map[string]string) []string {
	result := make([]string, 0, len(base)+len(values))
	for _, item := range base {
		key, _, _ := strings.Cut(item, "=")
		if _, replace := values[key]; !replace {
			result = append(result, item)
		}
	}
	for key, value := range values {
		result = append(result, key+"="+value)
	}
	return result
}
