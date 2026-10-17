//go:build darwin || linux

// Package pluginhost drives a retained Node plugin in an authorized Host process
// group. Plugins run with the user's privileges; this is not an isolation boundary.
// Control must verify/pin the package, validate its tool schema and authorize each
// exact operation before Execute. This package never makes policy decisions.
package pluginhost

import (
	"bufio"
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Canonical retained Node transport, embedded by the Go plugin host.
//
//go:embed assets/plugin-host.mjs
var hostSource string

const (
	maxTransport      = 2 * 1024 * 1024
	maxText           = 256 * 1024
	maxImage          = 1400000
	invocationTimeout = 30 * time.Second
	idleTimeout       = 30 * time.Minute
)

var ErrSessionEnded = errors.New("plugin session ended; explicitly start a new session")

// Config is a trusted, already verified Host-capable package snapshot. Identity
// is its pinned bundle digest, not a path or model-provided package reference.
// Session ownership (Agent/epoch), the eight-session limit and input-schema
// validation belong to the caller's execution manager.
type Config struct {
	Workspace       string
	Environment     map[string]string
	Node            string
	Identity        string
	Entrypoint      string
	Files           map[string]string
	Mode            string        // text or mixed, derived from model capabilities by Control
	ExecutionTarget string        // host (default) or sandbox under a trusted Python broker
	IdleTimeout     time.Duration // zero uses the 30-minute default
}

type Content struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MIMEType string `json:"mimeType,omitempty"`
	Width    int    `json:"width,omitempty"`
	Height   int    `json:"height,omitempty"`
}

type Result struct {
	SessionID string    `json:"session_id"`
	Content   []Content `json:"content"`
	IsError   bool      `json:"isError,omitempty"`
	Error     string    `json:"error,omitempty"`
}

// Client owns one immutable package/mode/Host session. Ordinary JavaScript errors
// are Results and preserve state; transport failure, cancellation and timeout
// invalidate the session. Nothing is retried, including after uncertain effects.
type Client struct {
	mu         sync.Mutex
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	stdout     io.ReadCloser
	reader     *bufio.Reader
	wait       chan error
	directory  string
	id, mode   string
	target     string
	idle       time.Duration
	touched    time.Time
	ended      bool
	cleanupErr error
}

func New(config Config) (*Client, error) {
	if config.ExecutionTarget == "" {
		config.ExecutionTarget = "host"
	}
	if config.ExecutionTarget != "host" && config.ExecutionTarget != "sandbox" {
		return nil, errors.New("invalid trusted plugin execution target")
	}
	if config.IdleTimeout < 0 {
		return nil, errors.New("invalid plugin idle timeout")
	}
	if config.IdleTimeout == 0 {
		config.IdleTimeout = idleTimeout
	}
	if config.Identity == "" || (config.Mode != "text" && config.Mode != "mixed") {
		return nil, errors.New("plugin requires pinned identity and text/mixed mode")
	}
	if len(config.Files) == 0 || len(config.Files) > 32 {
		return nil, errors.New("invalid plugin file count")
	}
	if _, ok := config.Files[config.Entrypoint]; !ok {
		return nil, errors.New("plugin entrypoint missing")
	}
	size := 0
	for name, source := range config.Files {
		if !safePath(name) {
			return nil, errors.New("plugin path escapes package")
		}
		size += len(source)
	}
	if size > 512*1024 {
		return nil, errors.New("plugin package too large")
	}
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return nil, err
	}
	directory, err := os.MkdirTemp("", "pot-go-plugin-")
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.RemoveAll(directory)
		}
	}()
	for name, source := range config.Files {
		path := filepath.Join(directory, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, []byte(source), 0600); err != nil {
			return nil, err
		}
	}
	node := config.Node
	if node == "" {
		node = "node"
	}
	cmd := exec.Command(node, "--input-type=module", "-e", hostSource, filepath.Join(directory, config.Entrypoint))
	cmd.Env = hostEnvironment(directory)
	if config.Workspace != "" {
		cmd.Env = append(cmd.Env, "POT_EXECUTION_WORKSPACE="+config.Workspace)
	}
	for name, value := range config.Environment {
		for i := len(cmd.Env) - 1; i >= 0; i-- {
			if strings.HasPrefix(cmd.Env[i], name+"=") {
				cmd.Env = append(cmd.Env[:i], cmd.Env[i+1:]...)
			}
		}
		cmd.Env = append(cmd.Env, name+"="+value)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	input, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		_ = input.Close()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		_ = input.Close()
		_ = output.Close()
		return nil, err
	}
	c := &Client{cmd: cmd, stdin: input, stdout: output, reader: bufio.NewReader(output), wait: make(chan error, 1), directory: directory, id: hex.EncodeToString(id), mode: config.Mode, target: config.ExecutionTarget, idle: config.IdleTimeout, touched: time.Now()}
	go func() { c.wait <- cmd.Wait() }()
	ok = true
	return c, nil
}

func safePath(name string) bool {
	if name == "" || strings.ContainsAny(name, "\\\x00") || filepath.IsAbs(name) {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func hostEnvironment(directory string) []string {
	var env []string
	for _, key := range []string{"PATH", "HOME", "TEMP", "TMP", "LANG", "LC_ALL", "POT_BROWSER_EXECUTABLE"} {
		if value, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+value)
		}
	}
	return append(env, "TMPDIR="+directory)
}

// Execute requires exact-operation Host authorization from the caller each time.
// Reset uses Close instead. Input session_id, when present, must match this client.
func (c *Client) Execute(ctx context.Context, tool string, input map[string]any) (Result, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ended {
		return Result{}, errors.Join(ErrSessionEnded, c.cleanupErr)
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if time.Since(c.touched) >= c.idle {
		return Result{}, errors.Join(ErrSessionEnded, c.stop(false))
	}
	if tool == "" || len(tool) > 64 {
		return Result{}, errors.New("invalid plugin tool")
	}
	if input == nil {
		return Result{}, errors.New("plugin input must be an object")
	}
	if id, ok := input["session_id"]; ok && id != c.id {
		return Result{}, errors.New("plugin session identity mismatch")
	}
	if input["reset"] == true {
		return Result{}, errors.New("use Close to dispose the existing session")
	}
	if code, ok := input["code"].(string); ok && len(code) > 32768 {
		return Result{}, errors.New("plugin code exceeds 32 KiB")
	}
	request, err := json.Marshal(map[string]any{"tool": tool, "input": input, "mode": c.mode, "execution_target": c.target})
	if err != nil {
		return Result{}, err
	}
	if len(request)+1 > maxTransport {
		return Result{}, errors.New("plugin request exceeds transport limit")
	}
	c.touched = time.Now()
	callCtx, cancel := context.WithTimeout(ctx, invocationTimeout)
	defer cancel()
	type response struct {
		result Result
		err    error
	}
	completed := make(chan response, 1)
	go func() { value, err := c.exchange(request); completed <- response{value, err} }()
	select {
	case value := <-completed:
		if callCtx.Err() == nil && value.err == nil {
			value.result.SessionID = c.id
			return value.result, nil
		}
		err = errors.Join(value.err, callCtx.Err())
	case <-callCtx.Done():
		err = callCtx.Err()
	}
	cleanupErr := c.stop(true)
	return Result{}, errors.Join(fmt.Errorf("plugin exchange failed; session ended, prior effects may have occurred: %w", err), cleanupErr)
}

func (c *Client) exchange(request []byte) (Result, error) {
	if _, err := c.stdin.Write(append(request, '\n')); err != nil {
		return Result{}, err
	}
	// LimitReader bounds allocation even when a plugin emits an unterminated line.
	line, err := bufio.NewReader(io.LimitReader(c.reader, maxTransport)).ReadBytes('\n')
	if err != nil || len(line) == 0 || line[len(line)-1] != '\n' {
		return Result{}, errors.New("plugin response truncated or process ended; outcome unknown")
	}
	var value Result
	if err := json.Unmarshal(line, &value); err != nil {
		return Result{}, err
	}
	if value.Content == nil || len(value.Content) > 1024 {
		return Result{}, errors.New("invalid plugin content")
	}
	textBytes, images := 0, 0
	for _, block := range value.Content {
		switch block.Type {
		case "text":
			textBytes += len(block.Text)
		case "image":
			images++
			if c.mode != "mixed" || images > 1 || !validImage(block) {
				return Result{}, errors.New("invalid plugin image or mode")
			}
		default:
			return Result{}, errors.New("unsupported plugin output")
		}
	}
	// JSON null and absent strings both decode into an empty Go string. Validate
	// text presence separately so malformed wire content cannot masquerade as text.
	var wire struct {
		Content []map[string]json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(line, &wire); err != nil {
		return Result{}, err
	}
	for i, block := range value.Content {
		if block.Type == "text" {
			var text any
			if err := json.Unmarshal(wire.Content[i]["text"], &text); err != nil {
				return Result{}, errors.New("invalid plugin text")
			}
			if _, ok := text.(string); !ok {
				return Result{}, errors.New("invalid plugin text")
			}
		}
	}
	if textBytes > maxText {
		return Result{}, errors.New("plugin text output exceeds limit")
	}
	return value, nil
}

func validImage(block Content) bool {
	if (block.MIMEType != "image/png" && block.MIMEType != "image/jpeg" && block.MIMEType != "image/webp") || len(block.Data) == 0 || len(block.Data) > maxImage || block.Width < 1 || block.Width > 4096 || block.Height < 1 || block.Height > 4096 {
		return false
	}
	_, err := base64.StdEncoding.Strict().DecodeString(block.Data)
	return err == nil
}

// Close gives dispose five seconds, kills remaining process-group descendants,
// and removes private files only after cleanup is confirmed. Errors must fence
// the owning execution; cancellation cannot undo completed plugin effects.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ended {
		return c.cleanupErr
	}
	return c.stop(false)
}

func (c *Client) stop(force bool) error {
	c.ended = true
	_ = c.stdin.Close()
	if force {
		_ = syscall.Kill(-c.cmd.Process.Pid, syscall.SIGKILL)
	}
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case <-c.wait:
	case <-timer.C:
		_ = syscall.Kill(-c.cmd.Process.Pid, syscall.SIGKILL)
		select {
		case <-c.wait:
		case <-time.After(time.Second):
			_ = c.stdout.Close()
			c.cleanupErr = errors.New("plugin process exit unconfirmed; temporary files retained")
			return c.cleanupErr
		}
	}
	_ = c.stdout.Close()
	for range 100 {
		err := syscall.Kill(-c.cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			c.cleanupErr = os.RemoveAll(c.directory)
			return c.cleanupErr
		}
		if err != nil {
			c.cleanupErr = fmt.Errorf("plugin cleanup unconfirmed: %w", err)
			return c.cleanupErr
		}
		time.Sleep(10 * time.Millisecond)
	}
	c.cleanupErr = errors.New("plugin process-group cleanup unconfirmed; temporary files retained")
	return c.cleanupErr
}

// SessionID is immutable and belongs only to this package/target/mode instance.
func (c *Client) SessionID() string { return c.id }
