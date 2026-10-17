package protocol

import (
	"encoding/json"
	"github.com/jjyr/pot/pkg/core"
	"testing"
)

func TestToolProjectionPreservesInputOutputAndTerminalStates(t *testing.T) {
	var tool ToolCall
	tool.Apply(core.Update{Type: "tool_started", ID: "child", ParentID: "parent", Name: "exec_command", Args: json.RawMessage(`{"cmd":"pwd"}`), AtMS: 10})
	target := core.HostTarget("local")
	tool.Apply(core.Update{Type: "tool_state", ID: "child", Phase: "waiting_approval", Target: &target, Workdir: "/project", AtMS: 20})
	tool.Apply(core.Update{Type: "tool_state", ID: "child", Phase: "running", AtMS: 30})
	tool.Apply(core.Update{Type: "tool_output", ID: "child", Stream: "stdout", Text: "one", AtMS: 40})
	tool.Apply(core.Update{Type: "tool_output", ID: "child", Stream: "stdout", Text: " two", AtMS: 45})
	tool.Apply(core.Update{Type: "tool_output", ID: "child", Stream: "stderr", Text: "warning", AtMS: 50})
	tool.Apply(core.Update{Type: "tool_finished", ID: "child", Result: json.RawMessage(`{"exit_code":0}`), AtMS: 60})
	if err := tool.Validate(); err != nil {
		t.Fatal(err)
	}
	if tool.Name != "exec_command" || tool.ParentID != "parent" || string(tool.Args) != `{"cmd":"pwd"}` || len(tool.Output) != 2 || tool.Output[0].Text != "one two" || tool.Output[1].Stream != "stderr" || tool.Phase != "completed" || tool.RunningAtMS != 30 || tool.FinishedAtMS != 60 || tool.Workdir != "/project" {
		t.Fatalf("lost tool detail: %+v", tool)
	}
	for _, test := range []struct {
		outcome core.Outcome
		phase   string
	}{{core.Cancelled, "stopped"}, {core.Outcome{Kind: "unknown", Error: "uncertain"}, "unknown"}, {core.Success, "unknown"}} {
		copy := tool
		copy.Apply(core.Update{Type: "turn_finished", Outcome: &test.outcome, AtMS: 80})
		if copy.Phase != test.phase {
			t.Fatal(copy)
		}
	}
	tool.Apply(core.Update{Type: "tool_state", Phase: "denied"})
	tool.Apply(core.Update{Type: "tool_finished", Failed: true, Result: json.RawMessage(`{"isError":true}`), AtMS: 90})
	if tool.Phase != "denied" {
		t.Fatal(tool)
	}
}
