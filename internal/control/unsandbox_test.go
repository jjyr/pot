package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/testutil"
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
	t.Parallel()
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
	t.Parallel()
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

func TestRealUnixSocketRequiresHostApproval(t *testing.T) {
	t.Parallel()
	if os.Getenv("POT_SANDBOX_INTEGRATION") != "1" {
		t.Skip("set POT_SANDBOX_INTEGRATION=1 for real OS sandbox qualification")
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
	workspace, err := os.MkdirTemp("/tmp", "pot-socket-approval-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(workspace)
	workspace, err = filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(workspace, "agent.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	d, execution, _ := localDispatcher(t)
	execution.Workspace = workspace
	if err := d.Permissions.Configure("local", MachinePolicy{ExecutionAccess: "ask"}); err != nil {
		t.Fatal(err)
	}
	worker, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	engine := executor.New(executor.Options{Home: workspace, Node: node, SandboxDirectory: sdk, ProcessWorker: []string{worker}})
	defer func() {
		if err := engine.Close(); err != nil {
			t.Error(err)
		}
	}()
	d.Host = engine
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	address, _ := json.Marshal(socket)
	script := `const s=require('node:net').connect(` + string(address) + `);s.once('connect',()=>{console.log('connected');s.destroy()});s.once('error',e=>{console.error(e.code);process.exitCode=23})`
	command := quote(node) + " -e " + quote(script)
	dispatch := func(id, text, decision string) (map[string]any, error) {
		t.Helper()
		args := map[string]any{"cmd": text, "login": false, "yield_time_ms": 1000}
		if decision != "" {
			args["request_unsandboxed"] = true
			args["justification"] = "Connect to the synthetic host agent socket"
		}
		encoded, _ := json.Marshal(args)
		ctx, cancel := context.WithTimeout(context.Background(), testutil.Timeout(t, 10*time.Second))
		defer cancel()
		approvals := 0
		raw, err := d.Dispatch(ctx, execution, modelprotocol.ToolCall{ID: id, Name: "exec_command", Arguments: encoded}, func(update core.Update) error {
			if update.Phase != "waiting_approval" {
				return nil
			}
			approvals++
			pending := d.Permissions.Pending(d.Owner)
			if decision == "" || len(pending) != 1 {
				t.Fatal("unexpected approval", pending)
			}
			return d.Permissions.Decide(d.Owner, pending[0].ID, decision)
		})
		if (decision == "" && approvals != 0) || (decision != "" && approvals != 1) {
			t.Fatal("missing or unexpected approval", id, approvals)
		}
		var result map[string]any
		if err == nil {
			if err := json.Unmarshal(raw, &result); err != nil {
				t.Fatal(err)
			}
		}
		return result, err
	}
	checkSandbox := func(id, text string) {
		t.Helper()
		result, err := dispatch(id, text, "")
		if err != nil || result["execution_mode"] != "sandbox" || result["exit_code"] != float64(23) || (!strings.Contains(fmt.Sprint(result["stderr"]), "EPERM") && !strings.Contains(fmt.Sprint(result["stderr"]), "EACCES")) {
			t.Fatal("socket access did not stay sandboxed and denied", result, err)
		}
	}
	checkSandbox("default", command)
	if _, err := dispatch("denied", command, "deny"); !errors.Is(err, ErrHostDenied) {
		t.Fatal("owner denial ignored", err)
	}
	checkSandbox("after-denial", command)
	result, err := dispatch("approved", command, "allow_task")
	if err != nil || result["execution_mode"] != "host" || result["exit_code"] != float64(0) || result["stdout"] != "connected\n" {
		t.Fatal("approved host access failed", result, err)
	}
	checkSandbox("different-command", " "+command)
	d.Turn = "next-turn"
	checkSandbox("next-turn", command)
}

func TestPreparedExecutionFailureIsVisibleToModel(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	for _, policy := range []string{"ask", "allow", "deny"} {
		section := executionEnvironment(MachinePolicy{ExecutionAccess: policy, Revision: 3}, core.RuntimeTarget("runner"), "/destination/workspace")
		var data map[string]any
		if section.Key != "execution-environment" || json.Unmarshal([]byte(section.Text), &data) != nil || data["runtime_id"] != "runner" || data["workspace"] != "/destination/workspace" || data["machine_policy"] == nil {
			t.Fatal(section)
		}
	}
}
