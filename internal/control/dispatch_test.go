package control

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/executor"
	"github.com/jjyr/pot/pkg/modelprotocol"
	"os"
	"path/filepath"
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

func (uncertainEngine) Execute(context.Context, executor.Request, func(executor.Event) error) (map[string]any, error) {
	return nil, core.ErrCleanupUnconfirmed
}
func TestUncertainHostCleanupFencesNewCall(t *testing.T) {
	d, e, _ := localDispatcher(t)
	d.Host = uncertainEngine{}
	call := modelprotocol.ToolCall{ID: "uncertain", Name: "exec_command", Arguments: json.RawMessage(`{"cmd":"true","login":false}`)}
	if _, err := d.Dispatch(context.Background(), e, call, nil); !core.IsCleanupUnconfirmed(err) {
		t.Fatal(err)
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
	for _, decision := range []string{"allow_once", "deny"} {
		t.Run(decision, func(t *testing.T) {
			d, e, _ := localDispatcher(t)
			if err := d.Permissions.Configure("local", MachinePolicy{ExecutionAccess: "ask"}); err != nil {
				t.Fatal(err)
			}
			phases := []string{}
			call := modelprotocol.ToolCall{ID: "review", Name: "exec_command", Arguments: json.RawMessage(`{"cmd":"printf approved","login":false}`)}
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
