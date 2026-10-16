package control

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/jjyr/pot/internal/tasks"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

func TestRoutingCASQuotaFencesAndCompleteCatalog(t *testing.T) {
	s := taskResourceService(t)
	ctx := context.Background()
	settings := core.DefaultAgentSettings()
	settings.Plugins = map[string]core.PluginConfig{}
	models := []string{}
	for i := 0; i < 80; i++ {
		models = append(models, fmt.Sprintf("model-%03d", i))
	}
	profile, err := s.Profiles.Save(ctx, SaveProfile{RequestID: "profile", ProfileID: "fixture", Name: "Fixture", Model: models[0], Models: models, Endpoint: "https://example.test", Protocol: "openai_chat", APIKey: "fixture", Agent: &settings, MaxOutputTokens: 100})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := s.SaveRouting(ctx, RoutingPolicy{ProfileLimits: map[string]uint64{profile.ID: 1}, ProviderLimits: map[string]uint64{profile.Provider: 2}})
	if err != nil || policy.Revision != 1 {
		t.Fatal(policy, err)
	}
	if _, err = s.SaveRouting(ctx, RoutingPolicy{ProfileLimits: map[string]uint64{}, ProviderLimits: map[string]uint64{}}); err == nil {
		t.Fatal("stale routing save accepted")
	}
	spec, err := s.Profiles.Resolve(profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	record := tasks.Record{LaunchSent: true, Config: spec}
	raw, _ := json.Marshal(record)
	if _, err = s.Tasks.DB().ExecContext(ctx, "INSERT INTO tasks VALUES('main','unknown','waiting',0,?)", string(raw)); err != nil {
		t.Fatal(err)
	}
	err = storage.Transaction(ctx, s.Tasks.DB(), func(tx *sql.Tx) error {
		admitted, err := s.admitTaskModel(ctx, tx, spec)
		if admitted {
			t.Error("uncertain original run released route capacity")
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Tasks.DB().ExecContext(ctx, "UPDATE tasks SET phase='failed' WHERE id='unknown'"); err != nil {
		t.Fatal(err)
	}
	err = storage.Transaction(ctx, s.Tasks.DB(), func(tx *sql.Tx) error {
		admitted, err := s.admitTaskModel(ctx, tx, spec)
		if !admitted {
			t.Error("settled run held quota")
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := s.ModelCatalog(ModelCatalogQuery{Profile: &profile.ID, Offset: 60, Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	row := catalog["profiles"].([]any)[0].(map[string]any)
	page := row["configured_models"].([]string)
	if len(page) != 20 || page[0] != "model-060" || row["configured_models_total"] != 80 {
		t.Fatal(row)
	}
	summary := s.modelContext()
	if len(summary) > 4100 || !strings.Contains(summary, "model-004") || strings.Contains(summary, "model-079") {
		t.Fatal("routing summary is unbounded or lost configured models")
	}
}
func TestTaskMachineSelectionUsesLabelsAndStableOriginalReceipt(t *testing.T) {
	s := taskResourceService(t)
	ctx := context.Background()
	if _, err := s.selectTaskMachine(ctx, ""); err == nil {
		t.Fatal("unlabelled local Runtime silently became Task runner")
	}
	if err := s.Machines.SetLabels(ctx, "local", []string{"task-runner"}); err != nil {
		t.Fatal(err)
	}
	machine, err := s.selectTaskMachine(ctx, "")
	if err != nil || machine != "local" {
		t.Fatal(machine, err)
	}
	// Tasks tests inject model/port contracts independently; this verifies the
	// default-choice retry never calls the dynamic placement resolver again.
	calls := 0
	spec := taskConfig(t)
	service, err := tasks.Open(t.TempDir()+"/tasks.sqlite", tasks.Options{ControlID: "control", RuntimeID: "local", ResolveMachine: func(context.Context, string) (string, error) {
		calls++
		if calls > 1 {
			return "changed", nil
		}
		return "original", nil
	}, ResolveModel: func(context.Context, string, protocol.CreateTask) (tasks.Selection, error) {
		return tasks.Selection{Config: spec, Plugins: []string{}}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	request := protocol.CreateTask{RequestID: "admit", Text: "Work"}
	first, err := service.Create(ctx, "main", request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Create(ctx, "main", request)
	if err != nil || first.Info.ID != second.Info.ID || second.Request.MachineID != "original" || calls != 1 {
		t.Fatal(first, second, calls, err)
	}
}
func taskConfig(t *testing.T) core.ResolvedSpec {
	t.Helper()
	settings := core.DefaultAgentSettings()
	settings.Plugins = map[string]core.PluginConfig{}
	profile := core.Profile{ID: "fixture", Revision: 1, Name: "Fixture", Provider: "fixture", Model: "fixture", AuthRef: "provider:fixture", Params: core.Parameters{MaxOutputTokens: 128, Agent: settings}, ToolPolicy: core.ToolPolicy{SummaryReadOnly: true, Unattended: "guest_vm"}}
	spec, err := profile.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	spec.GatewayProtocol = "openai_chat"
	if err = spec.Rehash(); err != nil {
		t.Fatal(err)
	}
	return spec
}
