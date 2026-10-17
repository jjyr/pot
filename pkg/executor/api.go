// Package executor runs already-authorized commands in one Agent/epoch scope.
// It contains no routing, credential ownership or approval decisions. Create a separate
// Engine for each execution owner; process handles cannot survive restart.
package executor

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/jjyr/pot/pkg/core"
)

type ShellSettings = core.ShellSettings

var DefaultShellSettings = core.DefaultShellSettings
var ErrCleanupUnconfirmed = core.ErrCleanupUnconfirmed

type Request struct {
	ID        string             `json:"id"`
	Operation string             `json:"operation"`
	CWD       string             `json:"cwd"`
	Policy    core.ShellSettings `json:"policy"`
	// Environment is transient and must never be included in durable receipts.
	Environment map[string]string `json:"environment,omitempty"`
	Args        map[string]any    `json:"args"`
}

// Event preserves the serde externally-tagged ToolOutput update wire format.
type Event struct {
	ToolOutput struct {
		ID     string `json:"id"`
		Stream string `json:"stream"`
		Text   string `json:"text"`
	} `json:"ToolOutput"`
}

func outputEvent(id, stream, text string) Event {
	var e Event
	e.ToolOutput.ID = id
	e.ToolOutput.Stream = stream
	e.ToolOutput.Text = text
	return e
}

type Options struct {
	Home string
	Node string
}

type Engine struct {
	mu        sync.Mutex
	lifecycle sync.Mutex
	inflight  sync.WaitGroup
	scope     context.Context
	cancel    context.CancelFunc
	draining  bool
	sessions  map[string]*process
	snapshots snapshots
	options   Options
	closed    bool
	fence     error
	boundCWD  string
	policy    *core.ShellSettings
	plugins   pluginSessions
}

func New(options Options) *Engine {
	if options.Home == "" {
		options.Home = os.Getenv("HOME")
	}

	scope, cancel := context.WithCancel(context.Background())
	return &Engine{sessions: map[string]*process{}, options: options, scope: scope, cancel: cancel}
}
func identity() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func (e *Engine) Execute(ctx context.Context, request Request, emit func(Event) error) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if err := request.Policy.Validate(); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(request.CWD) {
		return nil, errors.New("execution cwd must be absolute")
	}
	cwd, err := filepath.EvalSymlinks(request.CWD)
	if err != nil {
		return nil, err
	}
	request.CWD = cwd
	if info, err := os.Stat(cwd); err != nil || !info.IsDir() {
		return nil, errors.New("execution cwd must be a directory")
	}
	e.mu.Lock()
	if e.closed || e.draining || e.fence != nil {
		err := errors.Join(errors.New("execution scope ended"), e.fence)
		e.mu.Unlock()
		return nil, err
	}
	if e.boundCWD == "" {
		e.boundCWD = cwd
		p := request.Policy
		e.policy = &p
	} else if e.boundCWD != cwd || *e.policy != request.Policy {
		e.mu.Unlock()
		return nil, errors.New("execution scope configuration changed")
	}
	e.inflight.Add(1)
	scope := e.scope
	e.mu.Unlock()
	defer e.inflight.Done()
	callCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(scope, cancel)
	defer stop()
	ctx = callCtx
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateEnvironment(request.Environment); err != nil {
		return nil, err
	}
	if request.Operation == "inspect" {
		return e.Inspect()
	}
	if err := validate(request.Operation, request.Args); err != nil {
		return nil, err
	}
	var result map[string]any
	switch request.Operation {
	case "apply_patch":
		var report string
		report, err = ApplyPatch(request.CWD, request.Args["patch"].(string))
		result = map[string]any{"output": report}
	case "plugin_call":
		result, err = e.executePlugin(ctx, request)
	default:
		result, err = e.executeProcess(ctx, request, emit)
	}
	if errors.Is(err, ErrCleanupUnconfirmed) {
		e.fenceCleanup(err)
	}
	return result, err
}

func validate(name string, args map[string]any) error {
	if args == nil {
		return errors.New("tool arguments must be an object")
	}
	var fields string
	var required string
	switch name {
	case "exec_command":
		fields = "cmd shell login tty workdir timeout_ms max_output_chars yield_time_ms"
		required = "cmd"
	case "write_stdin":
		fields = "session_id chars close_stdin interrupt terminate rows cols yield_time_ms max_output_chars"
		required = "session_id"
	case "apply_patch":
		fields = "patch"
		required = "patch"
	case "plugin_call":
		return nil
	default:
		return errors.New("unknown execution tool")
	}
	for key, value := range args {
		if !strings.Contains(" "+fields+" ", " "+key+" ") {
			return fmt.Errorf("unknown tool argument %s", key)
		}
		switch key {
		case "cmd", "shell", "workdir", "session_id", "chars", "patch":
			if _, ok := value.(string); !ok {
				return fmt.Errorf("%s must be a string", key)
			}
		case "login", "tty", "close_stdin", "interrupt", "terminate":
			if _, ok := value.(bool); !ok {
				return fmt.Errorf("%s must be boolean", key)
			}
		default:
			if _, err := integer(value); err != nil {
				return fmt.Errorf("invalid %s", key)
			}
		}
	}
	if text, ok := args[required].(string); !ok || text == "" {
		return fmt.Errorf("missing string argument: %s", required)
	}
	for _, key := range []string{"cmd", "chars"} {
		if text, ok := args[key].(string); ok && len(text) > 1024*1024 {
			return fmt.Errorf("%s exceeds 1 MiB", key)
		}
	}
	for _, key := range []string{"rows", "cols"} {
		if value, ok := args[key]; ok {
			n, _ := integer(value)
			if n < 1 || n > 1000 {
				return fmt.Errorf("invalid %s", key)
			}
		}
	}
	return nil
}
func integer(value any) (uint64, error) {
	switch n := value.(type) {
	case json.Number:
		v, err := n.Int64()
		if err == nil && v >= 0 {
			return uint64(v), nil
		}
	case int:
		if n >= 0 {
			return uint64(n), nil
		}
	case int64:
		if n >= 0 {
			return uint64(n), nil
		}
	case uint64:
		return n, nil
	case float64:
		if n >= 0 && n <= float64(core.MaxSafeInteger) && n == float64(uint64(n)) {
			return uint64(n), nil
		}
	}
	return 0, errors.New("expected nonnegative integer")
}
func number(args map[string]any, key string, fallback, max uint64) (uint64, error) {
	n := fallback
	if value, ok := args[key]; ok {
		var err error
		n, err = integer(value)
		if err != nil {
			return 0, err
		}
	}
	if n == 0 || n > max {
		return 0, fmt.Errorf("%s exceeds configured limit", key)
	}
	return n, nil
}
func resolve(path, workspace string) (string, error) {
	if path == "" {
		path = workspace
	}
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part == ".." {
			return "", errors.New("parent path components are not allowed")
		}
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(workspace, path)
	}
	return filepath.EvalSymlinks(path)
}
func (e *Engine) fenceCleanup(err error) {
	if err == nil {
		return
	}
	e.mu.Lock()
	e.fence = errors.Join(e.fence, err)
	e.mu.Unlock()
}

func validateEnvironment(env map[string]string) error {
	if len(env) > 16 {
		return errors.New("too many credential environment variables")
	}
	total := 0
	for name, value := range env {
		total += len(name) + len(value)
		if !core.CredentialEnvironmentName(name) || strings.ContainsRune(value, 0) {
			return errors.New("invalid credential environment")
		}
	}
	if total > 256*1024 {
		return errors.New("credential environment exceeds limit")
	}
	return nil
}
func mergeEnvironment(base []string, extra map[string]string) []string {
	result := make([]string, 0, len(base)+len(extra))
	for _, entry := range base {
		name, _, _ := strings.Cut(entry, "=")
		if _, ok := extra[name]; !ok {
			result = append(result, entry)
		}
	}
	for name, value := range extra {
		result = append(result, name+"="+value)
	}
	return result
}
