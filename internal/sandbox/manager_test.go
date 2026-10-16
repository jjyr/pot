package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/core"
	"github.com/jjyr/pot/internal/executor"
)

// lifecycleDouble supplies only VM transitions. Guest execution is the actual
// Go executor on private test paths; these tests do not establish VM isolation.
type lifecycleDouble struct {
	mu         sync.Mutex
	root       string
	running    bool
	generation uint64
	shared     bool
	confirm    bool
	loseReply  bool
	executions int
	starts     int
	engines    map[string]*executor.Engine
}

func platformID() string { return Podman }
func (p *lifecycleDouble) Descriptor() Descriptor {
	return Descriptor{ID: platformID(), OS: "linux", HostOS: runtime.GOOS, Engine: "test-lifecycle", Capabilities: Capabilities{Shell: true, Files: true, SharedExecution: p.shared}}
}
func (p *lifecycleDouble) DefaultResources() Resources {
	return Resources{CPUs: 4, MemoryGiB: 8, DiskGiB: 32}
}
func (p *lifecycleDouble) ValidateResources(Instance, Resources) error { return nil }
func (p *lifecycleDouble) Workspace(instance Instance) string {
	return filepath.Join(p.root, instance.ID, "workspace")
}
func (p *lifecycleDouble) GuestWorkspace() string       { return "/data/workspace" }
func (p *lifecycleDouble) CleanupPolicy() CleanupPolicy { return ProcessContainment }
func (p *lifecycleDouble) BindResources(context.Context, Instance, PreparedResources) error {
	return nil
}
func (p *lifecycleDouble) Lifecycle(ctx context.Context, instance Instance, method string) (Status, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch method {
	case "start":
		if !p.running {
			p.generation++
			p.starts++
		}
		p.running = true
	case "stop", "delete":
		p.running = false
	}
	phase := "stopped"
	if p.running {
		phase = "running"
	}
	return Status{phase, p.generation}, nil
}
func (p *lifecycleDouble) Health(context.Context, Instance) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.running, nil
}
func (p *lifecycleDouble) Request(ctx context.Context, instance Instance, operation string, body json.RawMessage, timeout time.Duration) (json.RawMessage, error) {
	var request struct {
		ID        string             `json:"id"`
		Scope     string             `json:"scope"`
		Operation string             `json:"operation"`
		Tool      string             `json:"tool"`
		CWD       string             `json:"cwd"`
		Policy    core.ShellSettings `json:"policy"`
		Args      map[string]any     `json:"args"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		return nil, err
	}
	p.mu.Lock()
	if operation == "finish" || operation == "cancel" {
		engine := p.engines[request.ID]
		confirm := p.confirm
		if confirm {
			delete(p.engines, request.ID)
		}
		p.mu.Unlock()
		if confirm && engine != nil {
			if err := engine.Close(); err != nil {
				return nil, err
			}
		}
		return json.Marshal(map[string]any{"settled": confirm})
	}
	if operation == "prepare" {
		p.mu.Unlock()
		if request.Tool == "exec_command" {
			if request.Args["workdir"] == nil {
				request.Args["workdir"] = request.CWD
			}
			if request.Args["shell"] == nil {
				request.Args["shell"] = request.Policy.Executable
			}
			if request.Args["login"] == nil {
				request.Args["login"] = request.Policy.AllowLoginShell
			}
			if request.Args["tty"] == nil {
				request.Args["tty"] = false
			}
			if request.Args["yield_time_ms"] == nil && request.Args["timeout_ms"] == nil {
				request.Args["timeout_ms"] = request.Policy.DefaultTimeoutMS
			}
		}
		return json.Marshal(map[string]any{"args": request.Args})
	}
	if operation == "inspect" {
		engine := p.engines[request.ID]
		p.mu.Unlock()
		if engine == nil {
			return json.Marshal(map[string]any{"sessions": []string{}, "active": false, "ended": true})
		}
		result, err := engine.Inspect()
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	}
	if operation != "execute" {
		p.mu.Unlock()
		return nil, errors.New("unknown test guest operation")
	}
	p.executions++
	loseReply := p.loseReply
	p.loseReply = false
	cwd := filepath.Join(p.Workspace(instance), strings.TrimPrefix(request.CWD, p.GuestWorkspace()))
	os.MkdirAll(cwd, 0700)
	engine := p.engines[request.Scope]
	if engine == nil {
		engine = executor.New(executor.Options{Home: cwd, ExecutionTarget: "sandbox"})
		p.engines[request.Scope] = engine
	}
	p.mu.Unlock()
	if workdir, ok := request.Args["workdir"].(string); ok && strings.HasPrefix(workdir, p.GuestWorkspace()) {
		request.Args["workdir"] = filepath.Join(p.Workspace(instance), strings.TrimPrefix(workdir, p.GuestWorkspace()))
	}
	events := []executor.Event{}
	result, err := engine.Execute(ctx, executor.Request{ID: request.ID, Operation: request.Operation, CWD: cwd, Policy: request.Policy, Args: request.Args}, func(event executor.Event) error { events = append(events, event); return nil })
	if err != nil {
		return nil, err
	}
	if loseReply {
		return nil, errors.New("simulated lost acknowledgement after real command effect")
	}
	return json.Marshal(map[string]any{"result": result, "events": events})
}
func (p *lifecycleDouble) Shutdown(context.Context) error {
	p.mu.Lock()
	engines := p.engines
	p.engines = map[string]*executor.Engine{}
	p.mu.Unlock()
	var result error
	for _, engine := range engines {
		result = errors.Join(result, engine.Close())
	}
	return result
}
func setupManager(t *testing.T, shared bool) (*Manager, *lifecycleDouble, AccessGrant) {
	t.Helper()
	root, _ := filepath.EvalSymlinks(t.TempDir())
	guest, _ := filepath.EvalSymlinks(t.TempDir())
	provider := &lifecycleDouble{root: guest, shared: shared, confirm: true, engines: map[string]*executor.Engine{}}
	manager, err := Open(context.Background(), Config{Root: root, RuntimeID: "runtime", Providers: []Provider{provider}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { provider.Shutdown(context.Background()); manager.Close() })
	if _, err := manager.EnsureShared(context.Background()); err != nil {
		t.Fatal(err)
	}
	policy := core.DefaultShellSettings()
	policy.AllowLoginShell = false
	policy.ShellSnapshot = false
	policy.DefaultTimeoutMS = 2000
	policy.MaxTimeoutMS = 5000
	policy.DefaultYieldMS = 30
	policy.MaxYieldMS = 1000
	return manager, provider, AccessGrant{Target: core.SandboxTarget("runtime", SharedID), Owner: "assistant-owner", Turn: "turn", ThreadGeneration: 1, Policy: policy}
}
func execute(t *testing.T, m *Manager, grant AccessGrant, id, name string, args string) map[string]any {
	t.Helper()
	raw, err := m.Execute(context.Background(), grant, id, name, json.RawMessage(args), nil)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestReadOnlyInventoryAndExactDuplicateReconciliation(t *testing.T) {
	manager, provider, grant := setupManager(t, true)
	manager.Inventory(context.Background())
	if provider.starts != 0 {
		t.Fatal("viewing inventory started a VM")
	}
	prepared, err := manager.PrepareOperation(context.Background(), grant, "exec_command", json.RawMessage(`{"cmd":"printf x >> effects; printf done"}`))
	if err != nil {
		t.Fatal(err)
	}
	var args map[string]any
	json.Unmarshal(prepared, &args)
	if args["workdir"] != "/data/workspace" || args["login"] != false {
		t.Fatalf("normalization %s", prepared)
	}
	var group sync.WaitGroup
	for range 2 {
		group.Go(func() {
			result, err := manager.Execute(context.Background(), grant, "original", "exec_command", prepared, nil)
			if err != nil || !strings.Contains(string(result), "done") {
				t.Errorf("%s %v", result, err)
			}
		})
	}
	group.Wait()
	if provider.executions != 1 {
		t.Fatal("duplicate command dispatched", provider.executions)
	}
	data, err := os.ReadFile(filepath.Join(provider.root, SharedID, "workspace/effects"))
	if err != nil || string(data) != "x" {
		t.Fatalf("command effects %q %v", data, err)
	}
	if _, err := manager.Execute(context.Background(), grant, "original", "exec_command", json.RawMessage(`{"cmd":"printf different"}`), nil); err == nil {
		t.Fatal("accepted conflicting request identity")
	}
	if err := manager.Finish(context.Background(), grant.Owner); err != nil {
		t.Fatal(err)
	}
	result, err := manager.Execute(context.Background(), grant, "original", "exec_command", prepared, nil)
	if err != nil || !strings.Contains(string(result), "done") {
		t.Fatalf("receipt after closure %s %v", result, err)
	}
}
func TestSessionOwnerTargetEpochAndGeneration(t *testing.T) {
	manager, provider, grant := setupManager(t, true)
	result := execute(t, manager, grant, "open", "exec_command", `{"cmd":"read -r value; printf '%s' \"$value\"","yield_time_ms":10}`)
	session := result["session_id"].(string)
	if target, err := manager.SessionTarget(context.Background(), grant.Owner, grant.ThreadGeneration, session); err != nil || target != grant.Target {
		t.Fatal(target, err)
	}
	if _, err := manager.SessionTarget(context.Background(), "different", grant.ThreadGeneration, session); err == nil {
		t.Fatal("cross-owner session")
	}
	if _, err := manager.SessionTarget(context.Background(), grant.Owner, grant.ThreadGeneration+1, session); err == nil {
		t.Fatal("cross-epoch session")
	}
	args, _ := json.Marshal(map[string]any{"session_id": session, "chars": "hello\n", "yield_time_ms": 1000})
	first, err := manager.Execute(context.Background(), grant, "write", "write_stdin", args, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Execute(context.Background(), grant, "write", "write_stdin", args, nil)
	if err != nil || string(first) != string(second) {
		t.Fatal("completed write receipt not recoverable", err)
	}
	result = execute(t, manager, grant, "open2", "exec_command", `{"cmd":"sleep 60","yield_time_ms":10}`)
	session = result["session_id"].(string)
	provider.mu.Lock()
	for _, engine := range provider.engines {
		engine.Close()
	}
	provider.engines = map[string]*executor.Engine{}
	provider.generation++
	provider.mu.Unlock()
	args, _ = json.Marshal(map[string]any{"session_id": session, "yield_time_ms": 10})
	if _, err := manager.Execute(context.Background(), grant, "stale", "write_stdin", args, nil); err == nil {
		t.Fatal("accepted session from old guest generation")
	}
	foreign := grant
	foreign.Target = core.SandboxTarget("other-runtime", SharedID)
	if _, err := manager.Execute(context.Background(), foreign, "foreign", "exec_command", json.RawMessage(`{"cmd":"true"}`), nil); err == nil {
		t.Fatal("remote target fell back locally")
	}
}
func TestLostAcknowledgementQuarantineExplicitRecoveryAndNoReplay(t *testing.T) {
	manager, provider, grant := setupManager(t, true)
	provider.loseReply = true
	args := json.RawMessage(`{"cmd":"printf x >> effects"}`)
	if _, err := manager.Execute(context.Background(), grant, "lost", "exec_command", args, nil); !core.IsCleanupUnconfirmed(err) {
		t.Fatal(err)
	}
	if _, err := manager.Execute(context.Background(), grant, "lost", "exec_command", args, nil); !core.IsCleanupUnconfirmed(err) {
		t.Fatal("uncertain operation replayed", err)
	}
	if _, err := manager.Lifecycle(context.Background(), SharedID, "stop"); err == nil {
		t.Fatal("stopped uncertain environment without recovery")
	}
	if err := manager.Recover(context.Background(), SharedID); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Execute(context.Background(), grant, "lost", "exec_command", args, nil); !core.IsCleanupUnconfirmed(err) {
		t.Fatal("recovery replayed old command", err)
	}
	result := execute(t, manager, grant, "after-recovery", "exec_command", `{"cmd":"cat effects"}`)
	if result["stdout"] != "x" || provider.executions != 2 {
		t.Fatal(result, provider.executions)
	}
	if err := manager.Finish(context.Background(), grant.Owner); err != nil {
		t.Fatal(err)
	}
}
func TestRestartUnknownScopesLeaseGuardsAndExclusiveOwner(t *testing.T) {
	manager, provider, grant := setupManager(t, false)
	result := execute(t, manager, grant, "open", "exec_command", `{"cmd":"sleep 60","yield_time_ms":10}`)
	if result["session_id"] == nil {
		t.Fatal(result)
	}
	other := grant
	other.Owner = "other-owner"
	if _, err := manager.Execute(context.Background(), other, "other", "exec_command", json.RawMessage(`{"cmd":"true"}`), nil); err == nil {
		t.Fatal("exclusive VM admitted another owner")
	}
	if _, err := manager.Lifecycle(context.Background(), SharedID, "stop"); err == nil {
		t.Fatal("stopped VM with active scope lease")
	}
	root := manager.root
	if _, err := Open(context.Background(), Config{Root: root, RuntimeID: "runtime", Providers: []Provider{provider}}); err == nil {
		t.Fatal("adopted an already-owned Manager root")
	}
	manager.Close()
	reopened, err := Open(context.Background(), Config{Root: root, RuntimeID: "runtime", Providers: []Provider{provider}})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, err := reopened.Execute(context.Background(), grant, "after-restart", "exec_command", json.RawMessage(`{"cmd":"true"}`), nil); !core.IsCleanupUnconfirmed(err) {
		t.Fatal("restart replay/admission before reconciliation", err)
	}
	if err := reopened.Recover(context.Background(), SharedID); err != nil {
		t.Fatal(err)
	}
	if result := execute(t, reopened, grant, "after-proof", "exec_command", `{"cmd":"printf recovered"}`); result["stdout"] != "recovered" {
		t.Fatal(result)
	}
	if err := reopened.Finish(context.Background(), grant.Owner); err != nil {
		t.Fatal(err)
	}
}
func TestCancellationClosesOnlyOwnedScopeAndPreservesVM(t *testing.T) {
	manager, provider, grant := setupManager(t, true)
	other := grant
	other.Owner = "other-owner"
	other.Turn = "other-turn"
	result := execute(t, manager, other, "other-open", "exec_command", `{"cmd":"read value; printf '%s' \"$value\"","yield_time_ms":10}`)
	otherSession := result["session_id"].(string)
	ended := make(chan error, 1)
	go func() {
		_, err := manager.Execute(context.Background(), grant, "blocking", "exec_command", json.RawMessage(`{"cmd":"sleep 60"}`), nil)
		ended <- err
	}()
	deadline := time.Now().Add(time.Second)
	for {
		provider.mu.Lock()
		count := provider.executions
		provider.mu.Unlock()
		if count >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("execution did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := manager.Cancel(context.Background(), grant.Owner); err != nil {
		t.Fatal(err)
	}
	if err := <-ended; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if !provider.running {
		t.Fatal("scope cancellation stopped the shared VM")
	}
	args, _ := json.Marshal(map[string]any{"session_id": otherSession, "chars": "still-alive\n", "yield_time_ms": 1000})
	raw, err := manager.Execute(context.Background(), other, "other-write", "write_stdin", args, nil)
	if err != nil || !strings.Contains(string(raw), "still-alive") {
		t.Fatalf("other scope %s %v", raw, err)
	}
}

func TestShutdownCancelsAndDrainsBeforeClosingProviders(t *testing.T) {
	manager, provider, grant := setupManager(t, true)
	ended := make(chan error, 1)
	go func() {
		_, err := manager.Execute(context.Background(), grant, "blocking", "exec_command", json.RawMessage(`{"cmd":"sleep 60"}`), nil)
		ended <- err
	}()
	deadline := time.Now().Add(time.Second)
	for {
		provider.mu.Lock()
		started := provider.executions > 0
		provider.mu.Unlock()
		if started {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("execution did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := manager.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-ended:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	default:
		t.Fatal("Shutdown returned before dispatch drained")
	}
	if _, err := manager.Execute(ctx, grant, "late", "exec_command", json.RawMessage(`{"cmd":"printf late"}`), nil); err == nil {
		t.Fatal("dispatch admitted after Shutdown")
	}
	if _, err := manager.Acquire(ctx, SharedID, "late"); err == nil {
		t.Fatal("lease admitted after Shutdown")
	}
	provider.mu.Lock()
	remaining := len(provider.engines)
	provider.mu.Unlock()
	if remaining != 0 {
		t.Fatal("provider kept live scopes")
	}
}
