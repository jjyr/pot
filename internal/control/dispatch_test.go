package control

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/executor"
	"github.com/jjyr/pot/pkg/modelprotocol"
	"github.com/jjyr/pot/pkg/plugin"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func localDispatcher(t *testing.T) (*Dispatcher, core.ExecutionContext, *executor.Engine) {
	t.Helper()
	root := t.TempDir()
	p, err := OpenPermissions(filepath.Join(root, "permissions.json"))
	if err != nil {
		t.Fatal(err)
	}
	p.Enroll("local", "epoch")
	p.Configure("local", MachinePolicy{ExecutionAccess: "allow"})
	operations, err := OpenOperations(filepath.Join(root, "operations.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { operations.Close() })
	s := modelSession(t, "https://example.com/v1/chat/completions")
	engine := executor.New(executor.Options{Home: root})
	t.Cleanup(func() { engine.Close() })
	d := &Dispatcher{RuntimeID: "local", Owner: "assistant", Turn: "turn", Generation: 1, DefaultTarget: core.RuntimeTarget("local"), Spec: s.Spec, Permissions: p, Operations: operations, Host: engine}
	return d, core.ExecutionContext{Owner: "assistant", Workspace: root, Role: core.Main}, engine
}
func TestAuthorizedHostCommandReceiptNoDuplicateEffect(t *testing.T) {
	d, execution, engine := localDispatcher(t)
	call := modelprotocol.ToolCall{ID: "call-1", Name: "exec_command", Arguments: json.RawMessage(`{"cmd":"printf x >> effect; printf hello","login":false}`)}
	first, err := d.Dispatch(context.Background(), execution, call, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := d.Dispatch(context.Background(), execution, call, nil)
	if err != nil || string(first) != string(second) {
		t.Fatal(err, string(first), string(second))
	}
	body, err := os.ReadFile(filepath.Join(execution.Workspace, "effect"))
	if err != nil || string(body) != "x" {
		t.Fatal(string(body), err)
	}
	rebound := call
	rebound.Arguments = json.RawMessage(`{"cmd":"printf y >> effect","login":false}`)
	if _, err = d.Dispatch(context.Background(), execution, rebound, nil); err == nil {
		t.Fatal("rebound receipt")
	}
	foreign := execution
	foreign.Owner = "other"
	if _, err = d.Dispatch(context.Background(), foreign, call, nil); err == nil {
		t.Fatal("cross-owner operation")
	}
	if err = engine.Drain(); err != nil {
		t.Fatal(err)
	}
}

type uncertainEngine struct{}

func TestBrowserUnavailableDiagnosticSurvivesDispatchAndReceiptReplay(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node unavailable")
	}
	t.Setenv("POT_BROWSER_EXECUTABLE", filepath.Join(t.TempDir(), "missing-chrome"))
	d, execution, _ := localDispatcher(t)
	bundle, err := plugin.LoadPlugin("../../plugins/computer-use")
	if err != nil {
		t.Fatal(err)
	}
	d.Spec.Params.Agent.Plugins = map[string]core.PluginConfig{"computer-use": {Enabled: true, Bundle: &bundle}}
	if err = d.Spec.Rehash(); err != nil {
		t.Fatal(err)
	}
	call := modelprotocol.ToolCall{ID: "browser-unavailable", Name: "cua_repl", Arguments: json.RawMessage(`{"code":"nodeRepl.write('UNEXPECTED_USER_CODE')"}`)}
	for range 2 {
		raw, err := d.Dispatch(context.Background(), execution, call, nil)
		var result map[string]any
		if err != nil || json.Unmarshal(raw, &result) != nil || result["isError"] != true || result["code"] != "browser_unavailable" || result["execution_status"] != "not_started" || strings.Contains(string(raw), "UNEXPECTED_USER_CODE") {
			t.Fatal("browser preflight diagnostic lost at Agent boundary", string(raw), err)
		}
	}
}

func TestStartupRejectionsReachAgentAsNotStarted(t *testing.T) {
	for _, args := range []string{
		`{"cmd":"printf changed > marker","tty":true,"login":false}`,
		`{"cmd":"mkdir -p missing; printf changed > marker","workdir":"missing","login":false}`,
	} {
		d, execution, _ := localDispatcher(t)
		call := modelprotocol.ToolCall{ID: "rejected", Name: "exec_command", Arguments: json.RawMessage(args)}
		for range 2 {
			raw, err := d.Dispatch(context.Background(), execution, call, nil)
			var result map[string]any
			if err == nil || json.Unmarshal(raw, &result) != nil || result["execution_status"] != "not_started" || result["isError"] != true {
				t.Fatal("startup error lost through dispatch or receipt replay", string(raw), err)
			}
		}
		if _, err := os.Stat(filepath.Join(execution.Workspace, "marker")); !os.IsNotExist(err) {
			t.Fatal("rejected command had effects", err)
		}
	}
}

func (uncertainEngine) Execute(context.Context, executor.Request, func(executor.Event) error) (map[string]any, error) {
	return nil, core.ErrCleanupUnconfirmed
}
func TestUncertainHostCleanupFencesNewCall(t *testing.T) {
	d, e, _ := localDispatcher(t)
	d.Host = uncertainEngine{}
	call := modelprotocol.ToolCall{ID: "uncertain", Name: "exec_command", Arguments: json.RawMessage(`{"cmd":"true","login":false}`)}
	if raw, err := d.Dispatch(context.Background(), e, call, nil); !core.IsCleanupUnconfirmed(err) {
		t.Fatal(err)
	} else {
		var result map[string]any
		if json.Unmarshal(raw, &result) != nil || result["execution_status"] != "cleanup_unconfirmed" {
			t.Fatal("uncertain effects reported as not started", string(raw))
		}
	}
	call.ID = "new-call"
	if _, err := d.Dispatch(context.Background(), e, call, nil); !errors.Is(err, ErrOperationUnknown) {
		t.Fatal(err)
	}
}

func TestToolsCannotRetargetAnotherRuntime(t *testing.T) {
	d, e, _ := localDispatcher(t)
	call := modelprotocol.ToolCall{ID: "retarget", Name: "exec_command", Arguments: json.RawMessage(`{"cmd":"true","execution_target":{"runtime_id":"other"}}`)}
	if _, err := d.Dispatch(context.Background(), e, call, nil); err == nil {
		t.Fatal("tool changed fixed destination")
	}
}

func TestDispatchReportsApprovalAndActualTargetBeforeExecution(t *testing.T) {
	for _, decision := range []string{"allow_task", "deny"} {
		t.Run(decision, func(t *testing.T) {
			d, e, _ := localDispatcher(t)
			if err := d.Permissions.Configure("local", MachinePolicy{ExecutionAccess: "ask"}); err != nil {
				t.Fatal(err)
			}
			phases := []string{}
			call := modelprotocol.ToolCall{ID: "review", Name: "exec_command", Arguments: json.RawMessage(`{"cmd":"printf approved","login":false,"request_unsandboxed":true,"justification":"Access required by the execution contract test"}`)}
			_, err := d.Dispatch(context.Background(), e, call, func(update core.Update) error {
				if update.Type != "tool_state" {
					return nil
				}
				phases = append(phases, update.Phase)
				if update.Target == nil || *update.Target != d.DefaultTarget || update.Workdir != e.Workspace {
					t.Fatal(update)
				}
				if update.Phase == "waiting_approval" {
					pending := d.Permissions.Pending(d.Owner)
					if len(pending) != 1 {
						t.Fatal(pending)
					}
					return d.Permissions.Decide(d.Owner, pending[0].ID, decision)
				}
				return nil
			})
			expected := "running"
			if decision == "deny" {
				expected = "denied"
				if !errors.Is(err, ErrHostDenied) {
					t.Fatal(err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if len(phases) != 3 || phases[0] != "preparing" || phases[1] != "waiting_approval" || phases[2] != expected {
				t.Fatal(phases)
			}
		})
	}
}
