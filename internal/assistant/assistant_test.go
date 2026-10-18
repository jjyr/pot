package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/execution"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

func TestRegistryDurableCreationCASAndRootBinding(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "control.sqlite")
	first, err := OpenRegistry(path, "main")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	created, err := first.Create(ctx, "create-one", "Research")
	if err != nil || created.ID != "main" {
		t.Fatal(created, err)
	}
	second, err := OpenRegistry(path, "main")
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, registry := range []*Registry{first, second} {
		wg.Add(1)
		go func(r *Registry) {
			defer wg.Done()
			_, err := r.Rename(ctx, created.ID, "Writer", created.Revision)
			results <- err
		}(registry)
	}
	wg.Wait()
	close(results)
	wins := 0
	for err := range results {
		if err == nil {
			wins++
		}
	}
	if wins != 1 {
		t.Fatal("CAS winners", wins)
	}
	duplicate, err := first.Create(ctx, "create-one", "Research")
	if err != nil || duplicate.ID != created.ID || duplicate.Name != "Writer" {
		t.Fatal(duplicate, err)
	}
	if _, err := first.Create(ctx, "create-one", "Other"); err == nil {
		t.Fatal("conflicting creation accepted")
	}
	first.Close()
	reopened, err := OpenRegistry(path, "main")
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	definitions, err := reopened.Definitions(ctx)
	if err != nil || len(definitions) != 1 || definitions[0].Name != "Writer" {
		t.Fatal(definitions, err)
	}
	if wrong, err := OpenRegistry(path, "other"); err == nil {
		wrong.Close()
		t.Fatal("registry rebound to another root")
	}
}
func resolved(t *testing.T, model string) core.ResolvedSpec {
	t.Helper()
	settings := core.DefaultAgentSettings()
	settings.Plugins = map[string]core.PluginConfig{}
	profile := core.Profile{ID: "profile", Revision: 1, Name: "Fixture", Provider: "provider", Model: model, AuthRef: "provider:provider", Params: core.Parameters{MaxOutputTokens: 1000, Agent: settings}, ToolPolicy: core.ToolPolicy{SummaryReadOnly: true, Unattended: "runtime"}}
	spec, err := profile.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	return spec
}
func TestThreadConfigurationPreservesContextAndRules(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "assistant.sqlite")
	store, err := storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	first := resolved(t, "model-a")
	if err := InitializeThread(ctx, store, "main", first); err != nil {
		t.Fatal(err)
	}
	if err := InitializeThread(ctx, store, "other", first); err == nil {
		t.Fatal("owner binding changed")
	}
	next := resolved(t, "model-b")
	if err := AssignInitial(ctx, store, next); err != nil {
		t.Fatal(err)
	}
	body := json.RawMessage(`{"type":"context","version":2,"messages":[{"role":"user","content":"persist"}],"last_request":"request"}`)
	if err := store.SaveContext(ctx, body); err != nil {
		t.Fatal(err)
	}
	if err := AssignInitial(ctx, store, first); err == nil {
		t.Fatal("started conversation reassigned")
	}
	thread, err := ConfigureNextGeneration(ctx, store, 1, first)
	if err != nil || thread.Generation != 2 {
		t.Fatal(thread, err)
	}
	if _, err := ConfigureNextGeneration(ctx, store, 1, next); err == nil {
		t.Fatal("stale config accepted")
	}
	if loaded, err := store.LoadContext(ctx); err != nil || string(loaded) != string(body) {
		t.Fatal(string(loaded), err)
	}
	rules, err := LoadInstructions(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	rules, err = SaveInstructions(ctx, store, "Own assistant rules", rules.Revision)
	if err != nil {
		t.Fatal(err)
	}
	store.Close()
	store, err = storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	current, err := CurrentThread(ctx, store)
	if err != nil || current.Generation != 2 || current.Config.Model != "model-a" {
		t.Fatal(current, err)
	}
	saved, err := LoadInstructions(ctx, store)
	if err != nil || saved != rules {
		t.Fatal(saved, err)
	}
}

type driverFixture struct {
	manager         *execution.Manager
	configured      bool
	invalidIdentity bool
	inputs          []protocol.SendMessage
	results         []execution.Result
	errors          []error
	finishError     error
}

func (f *driverFixture) Configured() bool { return f.configured }
func (f *driverFixture) Identity(input protocol.SendMessage) execution.Identity {
	owner := "main"
	generation := uint64(1)
	if f.invalidIdentity {
		owner = ""
		generation = 0
	}
	return execution.Identity{Owner: owner, RequestID: input.RequestID, Generation: generation, Role: "assistant"}
}
func (f *driverFixture) Execute(ctx context.Context, input protocol.SendMessage) execution.Result {
	snapshot := f.manager.Snapshot()
	if len(snapshot) == 0 || snapshot[len(snapshot)-1].Phase != execution.Running {
		return execution.Result{Phase: execution.Interrupted, Err: errors.New("bypassed shared execution manager")}
	}
	f.inputs = append(f.inputs, input)
	return execution.Result{Phase: execution.Succeeded}
}
func (f *driverFixture) Failed(err error) { f.errors = append(f.errors, err) }
func (f *driverFixture) FinishTurn(_ context.Context, _ protocol.SendMessage, result execution.Result) error {
	f.results = append(f.results, result)
	return f.finishError
}
func TestDriverAlwaysUsesSharedManagerAndPreservesText(t *testing.T) {
	manager, err := execution.New("runtime", "boot", execution.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	fixture := &driverFixture{manager: manager, configured: true}
	input := make(chan protocol.SendMessage, 1)
	request := protocol.SendMessage{RequestID: "request", ConversationID: "main", Text: "  用户原文\n/skill:research\n"}
	input <- request
	close(input)
	if err := (Driver{manager, fixture}).Serve(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if len(fixture.inputs) != 1 || !fixture.inputs[0].Equal(request) || len(fixture.errors) != 0 || fixture.results[0].Phase != execution.Succeeded {
		t.Fatal(fixture)
	}
	fixture.invalidIdentity = true
	input = make(chan protocol.SendMessage, 1)
	input <- request
	close(input)
	if err := (Driver{manager, fixture}).Serve(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if len(fixture.inputs) != 1 || len(fixture.errors) != 1 {
		t.Fatal("invalid identity invoked Agent", fixture)
	}
}
