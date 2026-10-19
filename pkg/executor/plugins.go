package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"time"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/pluginhost"
	"github.com/jjyr/pot/pkg/processscope"
)

type pluginInstance struct {
	executionMode string
	revision      uint64
	client        *pluginhost.Client
	digest, mode  string
	environment   string
	touched       time.Time
	idle          time.Duration
	busy          int
}
type pluginSessions struct {
	mu        sync.Mutex
	instances map[string]*pluginInstance
	ctx       context.Context
	cancel    context.CancelFunc
	closing   bool
	fence     error
}

func (p *pluginSessions) reapLocked() error {
	for id, instance := range p.instances {
		if instance.busy == 0 && time.Since(instance.touched) >= instance.idle {
			if err := instance.client.Close(); err != nil {
				p.fence = errors.Join(p.fence, ErrCleanupUnconfirmed, err)
				return p.fence
			}
			delete(p.instances, id)
		}
	}
	return p.fence
}
func (p *pluginSessions) inventory() ([]string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.reapLocked(); err != nil {
		return nil, err
	}
	ids := []string{}
	for id := range p.instances {
		ids = append(ids, id)
	}
	return ids, nil
}
func (p *pluginSessions) close() error {
	p.mu.Lock()
	p.closing = true
	if p.cancel != nil {
		p.cancel()
	}
	instances := make([]*pluginInstance, 0, len(p.instances))
	for _, instance := range p.instances {
		instances = append(instances, instance)
	}
	p.mu.Unlock()
	var result error
	for _, instance := range instances {
		if err := instance.client.Close(); err != nil {
			result = errors.Join(result, ErrCleanupUnconfirmed, err)
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.fence = errors.Join(p.fence, result)
	if result == nil {
		p.instances = nil
		p.ctx = nil
		p.cancel = nil
	}
	p.closing = false
	return p.fence
}
func (e *Engine) executePlugin(ctx context.Context, r Request) (map[string]any, error) {
	for key := range r.Args {
		switch key {
		case "plugin", "tool", "input", "mode", "session_id":
		default:
			return nil, errors.New("unknown plugin envelope field")
		}
	}
	data, err := json.Marshal(r.Args["plugin"])
	if err != nil {
		return nil, err
	}
	var bundle core.PluginBundle
	if err := core.DecodeJSON(data, &bundle); err != nil {
		return nil, err
	}
	if err := bundle.Validate(); err != nil {
		return nil, err
	}
	mode, _ := r.Args["mode"].(string)
	if mode != "text" && mode != "mixed" {
		return nil, errors.New("invalid plugin mode")
	}

	input, ok := r.Args["input"].(map[string]any)
	if !ok {
		return nil, errors.New("plugin input must be an object")
	}
	tool, _ := r.Args["tool"].(string)
	if err := bundle.ValidateInput(tool, input); err != nil {
		return nil, err
	}
	rawSession, sessionPresent := r.Args["session_id"]
	session, hasSession := rawSession.(string)
	if sessionPresent && (!hasSession || session == "" || len(session) > 64) {
		return nil, errors.New("invalid plugin session ID")
	}
	inputID, hasInputID := input["session_id"].(string)
	if hasSession != hasInputID || session != inputID {
		return nil, errors.New("plugin session identity mismatch")
	}
	if input["reset"] == true && !hasSession {
		return nil, errors.New("reset requires existing session_id")
	}
	idle := time.Duration(r.Policy.SessionIdleTimeoutMS) * time.Millisecond
	if idle <= 0 {
		return nil, errors.New("plugin idle timeout exceeds supported duration")
	}
	environment, _ := core.HashJSON(r.Environment)
	p := &e.plugins
	p.mu.Lock()
	if p.closing || p.fence != nil {
		err := errors.Join(errors.New("plugin execution scope closing"), p.fence)
		p.mu.Unlock()
		return nil, err
	}
	if err := p.reapLocked(); err != nil {
		p.mu.Unlock()
		return nil, err
	}
	if p.instances == nil {
		p.instances = map[string]*pluginInstance{}
		p.ctx, p.cancel = context.WithCancel(context.Background())
	}
	instance := p.instances[session]
	if hasSession {
		if instance == nil {
			p.mu.Unlock()
			return nil, errors.New("plugin session expired; explicitly start a new REPL")
		}
		if instance.executionMode != r.Mode || instance.revision != r.Revision || instance.digest != bundle.Digest || instance.mode != mode || instance.environment != environment && input["reset"] != true {
			p.mu.Unlock()
			return nil, errors.New("plugin session binding or credentials changed; reset the session and start a new one")
		}
	} else {
		if len(p.instances) >= 8 {
			p.mu.Unlock()
			return nil, errors.New("plugin session limit reached")
		}
		markers := &processscope.Markers{}
		var prepare func(context.Context, *exec.Cmd, string) (func() error, error)
		if r.Mode == "sandbox" {
			prepare = func(_ context.Context, cmd *exec.Cmd, bundle string) (func() error, error) {
				cmd.Dir = r.CWD
				launcher := e.sandboxLauncher()
				launcher.Markers = markers
				finish, err := launcher.Prepare(ctx, cmd, r.CWD, append(append([]string{}, e.options.SandboxRead...), bundle), e.options.SandboxWrite, false)
				if err != nil {
					return nil, &PreparationError{Cause: err}
				}
				return finish, nil
			}
		}
		if r.Mode == "session" {
			p.mu.Unlock()
			return nil, errors.New("session authority cannot launch a plugin")
		}
		client, err := pluginhost.New(pluginhost.Config{Markers: markers, ProcessWorker: e.options.ProcessWorker, Admit: func() (func(), error) {
			if e.options.Admit != nil {
				return e.options.Admit(r.Revision)
			}
			return func() {}, nil
		}, Prepare: prepare, Workspace: r.CWD, Environment: r.Environment, Node: e.options.Node, Identity: bundle.Digest, Entrypoint: bundle.Manifest.Contributes.Tools.Entrypoint, Files: bundle.Files, Mode: mode, IdleTimeout: idle})
		if err != nil {
			p.mu.Unlock()
			return nil, err
		}
		instance = &pluginInstance{executionMode: r.Mode, revision: r.Revision, environment: environment, client: client, digest: bundle.Digest, mode: mode, touched: time.Now(), idle: idle}
		session = client.SessionID()
		p.instances[session] = instance
	}
	instance.touched = time.Now()
	instance.busy++
	scope := p.ctx
	if input["reset"] == true {
		delete(p.instances, session)
		p.mu.Unlock()
		if err := instance.client.Close(); err != nil {
			err = errors.Join(ErrCleanupUnconfirmed, err)
			e.fenceCleanup(err)
			return nil, err
		}
		return map[string]any{"closed": true}, nil
	}
	p.mu.Unlock()
	callCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(scope, cancel)
	defer stop()
	result, err := instance.client.Execute(callCtx, tool, input)
	p.mu.Lock()
	instance.busy--
	p.mu.Unlock()
	if err != nil {
		cleanup := instance.client.Close()
		p.mu.Lock()
		delete(p.instances, session)
		p.mu.Unlock()
		if cleanup != nil {
			err = errors.Join(err, ErrCleanupUnconfirmed, cleanup)
			e.fenceCleanup(err)
		}
		return nil, err
	}
	if bundle.Manifest.Contributes.Tools.SessionMode == "call" {
		cleanup := instance.client.Close()
		p.mu.Lock()
		delete(p.instances, session)
		p.mu.Unlock()
		if cleanup != nil {
			e.fenceCleanup(errors.Join(ErrCleanupUnconfirmed, cleanup))
			return nil, errors.Join(ErrCleanupUnconfirmed, cleanup)
		}
		result.SessionID = ""
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	var value map[string]any
	if err := json.Unmarshal(encoded, &value); err != nil {
		return nil, fmt.Errorf("plugin result conversion: %w", err)
	}
	return value, nil
}
