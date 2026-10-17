package control

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/executor"
	"github.com/jjyr/pot/pkg/modelprotocol"
)

type modeEngine struct{ calls []executor.Request }

func (e *modeEngine) Execute(_ context.Context, r executor.Request, _ func(executor.Event) error) (map[string]any, error) {
	e.calls = append(e.calls, r)
	return map[string]any{"execution_mode": r.Mode, "execution_status": "finished", "stdout": "proof", "exit_code": 0}, nil
}

func TestUnsandboxRequestValidationPrecedesApproval(t *testing.T) {
	d, e, _ := localDispatcher(t)
	engine := &modeEngine{}
	d.Host = engine
	for _, args := range []string{
		`{"cmd":"true","execution_mode":"host"}`,
		`{"cmd":"true","execution_mode":"default"}`,
		`{"cmd":"true","request_unsandboxed":"true","justification":"reason"}`,
		`{"cmd":"true","request_unsandboxed":null}`,
		`{"cmd":"true","request_unsandboxed":true}`,
		`{"cmd":"true","request_unsandboxed":true,"justification":"  "}`,
		`{"cmd":"true","request_unsandboxed":true,"justification":3}`,
		`{"cmd":"true","request_unsandboxed":true,"justification":"reason","unexpected":true}`,
	} {
		if _, err := d.Dispatch(context.Background(), e, modelprotocol.ToolCall{ID: "invalid", Name: "exec_command", Arguments: json.RawMessage(args)}, nil); err == nil {
			t.Fatal("invalid request accepted", args)
		}
	}
	if len(engine.calls) != 0 || len(d.Permissions.Pending(d.Owner)) != 0 {
		t.Fatal("invalid request crossed admission")
	}
}

func TestUnsandboxRequestSelectsPermissionRatherThanExecutionMode(t *testing.T) {
	d, e, _ := localDispatcher(t)
	engine := &modeEngine{}
	d.Host = engine
	if err := d.Permissions.Configure("local", MachinePolicy{ExecutionAccess: "ask"}); err != nil {
		t.Fatal(err)
	}
	call := modelprotocol.ToolCall{ID: "ordinary", Name: "exec_command", Arguments: json.RawMessage(`{"cmd":"true","login":false}`)}
	if _, err := d.Dispatch(context.Background(), e, call, nil); err != nil {
		t.Fatal(err)
	}
	if engine.calls[0].Mode != "sandbox" {
		t.Fatal("ordinary protected work requested host")
	}
	call.ID = "approve"
	call.Arguments = json.RawMessage(`{"cmd":"true","login":false,"request_unsandboxed":true,"justification":"specific host access"}`)
	if _, err := d.Dispatch(context.Background(), e, call, func(u core.Update) error {
		if u.Phase == "waiting_approval" {
			p := d.Permissions.Pending(d.Owner)
			if len(p) != 1 {
				t.Fatal(p)
			}
			return d.Permissions.Decide(d.Owner, p[0].ID, "allow_always")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if engine.calls[1].Mode != "host" {
		t.Fatal("approved request did not execute on host")
	}
	for _, r := range engine.calls {
		if _, ok := r.Args["request_unsandboxed"]; ok {
			t.Fatal("permission request leaked into executor arguments")
		}
	}
	call.ID = "saved"
	call.Arguments = json.RawMessage(`{"cmd":"true","login":false,"request_unsandboxed":false}`)
	if _, err := d.Dispatch(context.Background(), e, call, nil); err != nil {
		t.Fatal(err)
	}
	if engine.calls[2].Mode != "host" {
		t.Fatal("false incorrectly forced sandbox despite permanent grant")
	}
}

func TestPreparedExecutionFailureIsVisibleToModel(t *testing.T) {
	d, e, _ := localDispatcher(t)
	d.Host = preparationFailureEngine{}
	raw, err := d.Dispatch(context.Background(), e, modelprotocol.ToolCall{ID: "prepare", Name: "exec_command", Arguments: json.RawMessage(`{"cmd":"true","login":false}`)}, nil)
	if err == nil {
		t.Fatal("missing failure")
	}
	var result map[string]any
	if json.Unmarshal(raw, &result) != nil || result["execution_status"] != "preparation_failed" || result["execution_mode"] != "host" || result["isError"] != true {
		t.Fatal(string(raw), err)
	}
}

type preparationFailureEngine struct{}

func (preparationFailureEngine) Execute(context.Context, executor.Request, func(executor.Event) error) (map[string]any, error) {
	return map[string]any{"execution_status": "preparation_failed"}, &executor.PreparationError{Cause: errors.New("sandbox dependency missing")}
}

func TestExecutionEnvironmentReportsDestinationPolicy(t *testing.T) {
	for _, policy := range []string{"ask", "allow", "deny"} {
		section := executionEnvironment(MachinePolicy{ExecutionAccess: policy, Revision: 3}, core.RuntimeTarget("runner"), "/destination/workspace")
		var data map[string]any
		if section.Key != "execution-environment" || json.Unmarshal([]byte(section.Text), &data) != nil || data["runtime_id"] != "runner" || data["workspace"] != "/destination/workspace" || data["machine_policy"] == nil {
			t.Fatal(section)
		}
	}
}
