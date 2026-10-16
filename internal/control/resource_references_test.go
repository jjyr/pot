package control

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/jjyr/pot/internal/core"
	"github.com/jjyr/pot/internal/protocol"
	"github.com/jjyr/pot/internal/runner"
	"github.com/jjyr/pot/internal/schedule"
)

func TestSandboxReferencesIncludeAllSchedulesAndOriginalRemoteRunnerArtifacts(t *testing.T) {
	s := taskResourceService(t)
	ctx := context.Background()
	if err := s.Machines.SetLabels(ctx, "local", []string{"task-runner"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Registry.Create(ctx, "create", "Pot"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 75; i++ {
		id := fmt.Sprintf("plan-%03d", i)
		machine := "local"
		if i == 74 {
			machine = ""
		}
		plan := schedule.Plan{Info: protocol.ScheduleInfo{ID: id, Spec: protocol.ScheduleSpec{MachineID: machine, Weekdays: []uint32{}}}, Generation: 1}
		raw, _ := json.Marshal(plan)
		if _, err := s.Tasks.DB().ExecContext(ctx, "INSERT INTO schedules(assistant_id,id,body)VALUES(?,?,?)", "main", id, string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	settings := core.DefaultAgentSettings()
	settings.Plugins = map[string]core.PluginConfig{}
	profile := core.Profile{ID: "profile", Revision: 1, Name: "Fixture", Provider: "mock", Model: "model", AuthRef: "provider:mock", Params: core.Parameters{MaxOutputTokens: 100, Agent: settings}, ToolPolicy: core.ToolPolicy{SummaryReadOnly: true, Unattended: "guest_vm"}}
	spec, err := profile.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	launch := runner.Launch{ControlID: "remote", RuntimeID: "local", AssistantID: "assistant", TaskID: "remote-task", RunID: "run", Generation: 1, Config: spec, Plugins: []string{}, Text: "work", ExecutionTarget: core.SandboxTarget("local", "saved")}
	if _, err = s.RunnerJournal.Accept(ctx, launch); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RunnerJournal.Claim(ctx, "remote", "remote-task", "run"); err != nil {
		t.Fatal(err)
	}
	if err = s.RunnerJournal.Finish(ctx, "remote", "remote-task", "run", runner.Terminal{Outcome: core.Success}); err != nil {
		t.Fatal(err)
	}
	refs, err := s.SandboxReferences(ctx, "saved", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 76 || !slices.Contains(refs, "schedule:plan-074") || !slices.Contains(refs, "task:remote-task") {
		t.Fatal(refs)
	}
	retained, err := s.SandboxReferences(ctx, "saved", false)
	if err != nil || len(retained) != 1 || retained[0] != "task:remote-task" {
		t.Fatal(retained, err)
	}
}
