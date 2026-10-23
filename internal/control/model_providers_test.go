package control

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/tasks"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/modeldirectory"
	"github.com/jjyr/pot/pkg/modelprotocol"
	"github.com/jjyr/pot/pkg/protocol"
)

func installMetadataFixture(t *testing.T, s *Service) string {
	t.Helper()
	root := s.options.PluginRoot
	target := filepath.Join(root, "acme")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	manifest := `{"schema_version":3,"name":"acme","version":"1","contributes":{"model_providers":{"providers":[{"id":"aggregate","name":"Aggregate","protocol":"openai_chat","capabilities":{"catalog":"plugin","allowances":true}}],"adapter":{"runtime":"node","execution":"control","entrypoint":"reader.mjs","files":["reader.mjs"],"credential_env":"ACME_KEY"}}}}`
	body := `export async function read(input) {
 if (process.env.ACME_KEY !== 'isolated-token') return {error:{code:'authorization'}};
 if(input.method==='list_models') return {catalog:{read_at:new Date().toISOString(),models:[{id:'acme/text',context_window:128000,max_output:16000,input_modalities:['text','image']}]}};
 return {allowances:{read_at:new Date().toISOString(),accounts:[{provider:'vendor',plan:'Pro',read_at:new Date().toISOString(),identity_quality:'ambiguous',windows:[{name:'weekly',used:91,identity_quality:'ambiguous'}]}]}};
 }`
	for name, value := range map[string]string{"plugin.json": manifest, "reader.mjs": body} {
		if err := os.WriteFile(filepath.Join(target, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return target
}

func TestProviderContributionCatalogCacheAndDisableGates(t *testing.T) {
	ctx := context.Background()
	s, _ := configurationService(t)
	target := installMetadataFixture(t, s)
	request := SaveProfile{RequestID: "aggregate-profile", Name: "Aggregate", Model: "acme/text", ProviderTypeID: "acme/aggregate", Endpoint: "https://aggregate.invalid/v1", Protocol: "openai_chat", APIKey: "isolated-token", ModelSettings: &core.ModelSettings{}}
	profile, err := s.Profiles.Save(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	route, err := s.Providers.Get(ctx, profile.Provider)
	if err != nil {
		t.Fatal(err)
	}
	if route.TypeID != "acme/aggregate" || route.Source == nil || route.Source.ID != "acme" {
		t.Fatal(route)
	}
	checked, err := s.Profiles.Check(ctx, CheckProvider{ProfileID: profile.ID, Endpoint: route.Endpoint, Protocol: route.Protocol})
	if err != nil || checked.Catalog == nil || len(checked.Catalog.Models) != 1 {
		t.Fatal(checked, err)
	}
	spec, err := s.Profiles.Resolve(profile.ID)
	if err != nil || spec.Params.Agent.ContextWindow != 128000 || spec.Params.MaxOutputTokens != 16000 {
		t.Fatal(spec.Params, err)
	}
	allowances, err := s.Profiles.ReadAllowances(ctx, profile.ID, true)
	if err != nil || len(allowances.Accounts) != 1 || allowances.Accounts[0].Plan != "Pro" {
		t.Fatal(allowances, err)
	}
	// Successful metadata does not grant credential reuse for a different route.
	if _, err = s.Profiles.Check(ctx, CheckProvider{ProfileID: profile.ID, Endpoint: "https://other.invalid/v1", Protocol: route.Protocol}); err == nil {
		t.Fatal("changed endpoint reused saved credential")
	}
	// Package replacement fences the old catalog instead of treating it as facts
	// for a new adapter. The saved route's provenance remains unchanged.
	f := filepath.Join(target, "reader.mjs")
	raw, _ := os.ReadFile(f)
	if err = os.WriteFile(f, append(raw, []byte("\n// new adapter revision\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	refreshed, err := s.Profiles.Resolve(profile.ID)
	if err != nil || refreshed.Params.Agent.ContextWindow == 128000 {
		t.Fatal(refreshed.Params, err)
	}
	// Old immutable route snapshots still retain their original key; fresh model
	// selections and metadata reads must honor package enablement.
	if _, err = s.ManageExtension(ctx, "", ExtensionCommand{Action: "toggle", Kind: "plugins", ID: "acme", Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Profiles.Resolve(profile.ID); err == nil {
		t.Fatal("disabled source resolved")
	}
	if _, err = s.Profiles.ResolveAssistant(AssistantSelection{ProfileID: profile.ID, ProfileRevision: profile.Revision, Model: profile.Model}); err == nil {
		t.Fatal("disabled assistant source resolved")
	}
	if _, err = s.Profiles.ReadAllowances(ctx, profile.ID, false); err == nil {
		t.Fatal("disabled source used cached allowance")
	}
	if key, err := s.Providers.Credential(ctx, route); err != nil || string(key) != "isolated-token" {
		t.Fatal("immutable route mutated", err)
	} else {
		clear(key)
	}
	disk, _ := os.ReadFile(s.Profiles.path)
	if strings.Contains(string(disk), "provider_types") || strings.Contains(string(disk), "type_id") || strings.Contains(string(disk), "isolated-token") {
		t.Fatal("projection or credential persisted in profiles.json")
	}
}

func TestBuiltinProvidersUseTheSameEnabledContributionRegistry(t *testing.T) {
	ctx := context.Background()
	s, profile := configurationService(t)
	snapshot := s.Profiles.Snapshot()
	found := false
	for _, typ := range snapshot.ProviderTypes {
		if typ.ID == customProviderType {
			found = typ.Available && typ.PluginID == "standard-model-providers"
		}
	}
	if !found {
		t.Fatal(snapshot.ProviderTypes)
	}
	if _, err := s.ManageExtension(ctx, "", ExtensionCommand{Action: "toggle", Kind: "plugins", ID: "standard-model-providers", Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Profiles.Resolve(profile.ID); err == nil {
		t.Fatal("builtin provider bypassed disabled contribution")
	}
	if _, err := s.Profiles.ResolveProviderEndpoint(ctx, CheckProvider{Protocol: "openai_chat", Endpoint: "https://fixture.invalid/v1"}); err == nil {
		t.Fatal("endpoint resolution bypassed disabled contribution")
	}
	data, _ := json.Marshal(s.Profiles.Snapshot())
	if strings.Contains(string(data), "isolated-key") {
		t.Fatal("credential exposed")
	}
}

func TestAutomaticPluginCatalogProjectionTracksCurrentModels(t *testing.T) {
	ctx := context.Background()
	s, _ := configurationService(t)
	target := installMetadataFixture(t, s)
	profile, err := s.Profiles.Save(ctx, SaveProfile{RequestID: "automatic-catalog", Name: "Aggregate", Model: "acme/text", Models: []string{"removed-model"}, ProviderTypeID: "acme/aggregate", Endpoint: "https://aggregate.invalid/v1", Protocol: "openai_chat", APIKey: "isolated-token", ModelSettings: &core.ModelSettings{}})
	if err != nil {
		t.Fatal(err)
	}
	s.Profiles.refreshPluginCatalogs(ctx)
	assertCatalog := func(expected []string) {
		snapshot := s.Profiles.Snapshot()
		if !reflect.DeepEqual(snapshot.ConfiguredModels[profile.ID], expected) {
			t.Fatal(snapshot.ConfiguredModels[profile.ID], expected)
		}
		found := false
		for _, connection := range snapshot.Connections {
			if connection.ProfileID == profile.ID {
				found = connection.Catalog != nil && len(connection.Catalog.Models) > 0 && connection.Catalog.Models[0].ContextWindow == 128000
			}
		}
		if !found {
			t.Fatal("typed catalog was not projected")
		}
	}
	assertCatalog([]string{"acme/text"})
	// A changed package is read afresh. Current models replace stale discovered
	// rows, while the owner's configured default remains visible as intent.
	file := filepath.Join(target, "reader.mjs")
	raw, _ := os.ReadFile(file)
	raw = []byte(strings.ReplaceAll(string(raw), "id:'acme/text'", "id:'acme/new'"))
	if err = os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	s.Profiles.refreshPluginCatalogs(ctx)
	assertCatalog([]string{"acme/new", "acme/text"})
}

func TestExactConnectionLimitsPrecedePublicSuffixFactsBeforeSaving(t *testing.T) {
	ctx := context.Background()
	s, _ := configurationService(t)
	target := installMetadataFixture(t, s)
	file := filepath.Join(target, "reader.mjs")
	raw, _ := os.ReadFile(file)
	raw = []byte(strings.ReplaceAll(string(raw), "id:'acme/text'", "id:'proxy/first'"))
	if err := os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	// The public suffix 'first' has output 4000. This exact connection reports
	// 16000, so an explicit 8000 override is valid and must not be rejected early.
	checked, err := s.Profiles.Check(ctx, CheckProvider{ProviderTypeID: "acme/aggregate", Endpoint: "https://aggregate.invalid/v1", Protocol: "openai_chat", APIKey: "isolated-token"})
	if err != nil || checked.Catalog == nil {
		t.Fatal(checked, err)
	}
	output := uint64(8000)
	request := SaveProfile{RequestID: "exact-limits", Name: "Exact", Model: "proxy/first", ProviderTypeID: "acme/aggregate", Endpoint: "https://aggregate.invalid/v1", Protocol: "openai_chat", APIKey: "isolated-token", ModelSettings: &core.ModelSettings{MaxOutputTokens: &output}}
	profile, err := s.Profiles.Save(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := s.Profiles.Resolve(profile.ID)
	if err != nil || spec.Params.MaxOutputTokens != 8000 || spec.Params.Agent.ContextWindow != 128000 {
		t.Fatal(spec.Params, err)
	}
	before := len(s.Profiles.Snapshot().Profiles)
	badOutput := uint64(20000)
	request.RequestID = "invalid-exact-limits"
	request.ModelSettings = &core.ModelSettings{MaxOutputTokens: &badOutput}
	if _, err = s.Profiles.Save(ctx, request); err == nil {
		t.Fatal("published override above exact model maximum")
	}
	if len(s.Profiles.Snapshot().Profiles) != before {
		t.Fatal("invalid profile became visible")
	}
}

func TestPluginCatalogRefreshCancelsWithItsOwner(t *testing.T) {
	s, _ := configurationService(t)
	target := installMetadataFixture(t, s)
	if _, err := s.Profiles.Save(context.Background(), SaveProfile{RequestID: "cancel-catalog", Name: "Slow", Model: "acme/text", ProviderTypeID: "acme/aggregate", Endpoint: "https://aggregate.invalid/v1", Protocol: "openai_chat", APIKey: "isolated-token"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "reader.mjs"), []byte(`export async function read(){ await new Promise(resolve=>setTimeout(resolve,60000)); }`), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	s.Profiles.refreshPluginCatalogs(ctx)
	if time.Since(started) > 5*time.Second {
		t.Fatal("catalog child outlived cancellation")
	}
}

func TestDisabledProviderBlocksFreshDefaultTasksAndUnpreparedResources(t *testing.T) {
	ctx := context.Background()
	s, profile := configurationService(t)
	if _, err := s.Registry.Create(ctx, "task-owner", "Pot"); err != nil {
		t.Fatal(err)
	}
	spec, err := s.Profiles.Resolve(profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.Conversation(ctx, "main")
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Configure(ctx, spec, 0); err != nil {
		t.Fatal(err)
	}
	selection, err := s.selectTask(ctx, "main", protocol.CreateTask{})
	if err != nil {
		t.Fatal(err)
	}
	launch := tasks.Launch{ControlID: s.options.RuntimeID, RuntimeID: s.options.RuntimeID, AssistantID: "main", TaskID: "prepared-source", RunID: "run", Generation: 1, ExecutionTarget: core.RuntimeTarget(s.options.RuntimeID), Config: selection.Config, Plugins: selection.Plugins, Workspace: protocol.TaskWorkspace{Kind: "directory", ProjectDir: t.TempDir()}, Text: "work"}
	if _, err = s.RunJournal.Accept(ctx, launch); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RunJournal.Claim(ctx, launch.ControlID, launch.TaskID, launch.RunID); err != nil {
		t.Fatal(err)
	}
	resources, revision, err := s.prepareTaskResources(ctx, launch)
	if err != nil {
		t.Fatal(err)
	}
	queued := launch
	queued.TaskID = "unprepared-source"
	queued.RunID = "queued-run"
	if _, err = s.RunJournal.Accept(ctx, queued); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RunJournal.Claim(ctx, queued.ControlID, queued.TaskID, queued.RunID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ManageExtension(ctx, "", ExtensionCommand{Action: "toggle", Kind: "plugins", ID: "standard-model-providers", Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.selectTask(ctx, "main", protocol.CreateTask{}); err == nil {
		t.Fatal("disabled source accepted default task")
	}
	if _, err = s.currentTaskResource(ctx, queued); err == nil {
		t.Fatal("disabled source prepared queued task")
	}
	if _, _, err = s.prepareTaskResources(ctx, queued); err == nil {
		t.Fatal("disabled source pinned first resources")
	}
	reused, sameRevision, err := s.prepareTaskResources(ctx, launch)
	if err != nil || sameRevision != revision || reused.Config.SpecHash != resources.Config.SpecHash {
		t.Fatal("prepared immutable route changed", err)
	}
}

func TestLocalExecutionRejectsAnotherNodesModelConfiguration(t *testing.T) {
	ctx := context.Background()
	source, profile := configurationService(t)
	spec, err := source.Profiles.Resolve(profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	node := taskResourceService(t)
	launch := tasks.Launch{ControlID: "remote-control", RuntimeID: node.options.RuntimeID, AssistantID: "main", TaskID: "remote-resource", Plugins: []string{}, RunID: "run", Generation: 1, ExecutionTarget: core.RuntimeTarget(node.options.RuntimeID), Config: spec, Workspace: protocol.TaskWorkspace{Kind: "directory", ProjectDir: t.TempDir()}, Text: "work"}
	if _, err = node.TaskRuns.Launch(ctx, launch); err == nil {
		t.Fatal("accepted a centrally configured remote execution")
	}
	if _, err = node.prepareTask(ctx, tasks.Run{Launch: launch}); err == nil {
		t.Fatal("prepared another node's model configuration")
	}
	if runs, err := node.RunJournal.Runs(ctx); err != nil || len(runs) != 0 {
		t.Fatal("rejected launch left a receipt", runs, err)
	}
}

func TestSavedCredentialRotationFencesBothDiscoveryReaders(t *testing.T) {
	for _, pluginReader := range []bool{false, true} {
		name := "http"
		if pluginReader {
			name = "plugin"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			s, _ := configurationService(t)
			entered := make(chan string, 1)
			release := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				entered <- r.Header.Get("Authorization")
				<-release
				if pluginReader {
					fmt.Fprintf(w, `{"catalog":{"read_at":%q,"models":[{"id":"visible-to-a"}]}}`, time.Now().UTC().Format(time.RFC3339Nano))
				} else {
					fmt.Fprint(w, `{"data":[{"id":"visible-to-a"}]}`)
				}
			}))
			defer server.Close()
			defer func() {
				select {
				case <-release:
				default:
					close(release)
				}
			}()
			typeID := customProviderType
			if pluginReader {
				target := installMetadataFixture(t, s)
				body := fmt.Sprintf(`export async function read(){const response=await fetch(%q,{headers:{Authorization:'Bearer '+process.env.ACME_KEY}});return await response.json();}`, server.URL+"/catalog")
				if err := os.WriteFile(filepath.Join(target, "reader.mjs"), []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
				typeID = "acme/aggregate"
			}
			request := SaveProfile{RequestID: "scope-a", Name: "Scoped", Model: "manual", ProviderTypeID: typeID, Endpoint: server.URL + "/v1", Protocol: "openai_chat", APIKey: "token-for-a", ModelSettings: &core.ModelSettings{}}
			profile, err := s.Profiles.Save(ctx, request)
			if err != nil {
				t.Fatal(err)
			}
			finished := make(chan error, 1)
			go func() {
				_, err := s.Profiles.Check(ctx, CheckProvider{ProfileID: profile.ID, Endpoint: request.Endpoint, Protocol: request.Protocol})
				finished <- err
			}()
			select {
			case auth := <-entered:
				if auth != "Bearer token-for-a" {
					t.Fatal(auth)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("reader did not start")
			}
			request.RequestID = "scope-b"
			request.ProfileID = profile.ID
			request.ExpectedRevision = profile.Revision
			request.APIKey = "token-for-b"
			rotated, err := s.Profiles.Save(ctx, request)
			if err != nil {
				t.Fatal(err)
			}
			close(release)
			select {
			case err := <-finished:
				if err == nil {
					t.Fatal("old-scope discovery accepted after rotation")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("reader did not finish")
			}
			_, route, typ, err := s.Profiles.ProviderConnection(ctx, rotated.ID)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := s.Providers.ReadMetadata(ctx, route.ID, "list_models", typ.Identity.Digest)
			if err != nil || len(raw) != 0 {
				t.Fatal("A models cached under B route", string(raw), err)
			}
		})
	}
}

func TestMissingAdapterDoesNotDisableUnrelatedProviderTypes(t *testing.T) {
	ctx := context.Background()
	s, standard := configurationService(t)
	target := installMetadataFixture(t, s)
	pluginProfile, err := s.Profiles.Save(ctx, SaveProfile{RequestID: "broken-package", Name: "Plugin", Model: "acme/text", ProviderTypeID: "acme/aggregate", Endpoint: "https://aggregate.invalid/v1", Protocol: "openai_chat", APIKey: "isolated-token"})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(target, "reader.mjs")); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Profiles.Resolve(standard.ID); err != nil {
		t.Fatal("missing optional adapter blocked standard connection", err)
	}
	if _, err = s.Profiles.Resolve(pluginProfile.ID); err == nil {
		t.Fatal("broken adapter connection resolved")
	}
	snapshot := s.Profiles.Snapshot()
	found := false
	for _, typ := range snapshot.ProviderTypes {
		if typ.ID == "acme/aggregate" {
			found = true
			if typ.Available {
				t.Fatal("broken adapter available")
			}
		}
	}
	if !found {
		t.Fatal("unavailable source disappeared instead of showing its status")
	}
	if _, err = s.ManageExtension(ctx, "", ExtensionCommand{Action: "toggle", Kind: "plugins", ID: "acme", Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Profiles.Resolve(standard.ID); err != nil {
		t.Fatal("disabled missing adapter blocked standard connection", err)
	}
}

func TestDeclarativeDirectoryReaderUsesLocalFactsWithoutCredentials(t *testing.T) {
	s, _ := configurationService(t)
	// Use the bundled, ready-connection directory rather than the limits-only
	// fixture that deliberately excludes inference-ready model declarations.
	directory, err := modeldirectory.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s.Profiles.directory = directory
	root := s.options.PluginRoot
	manifest := `{"schema_version":3,"name":"local-facts","version":"1","contributes":{"model_providers":{"providers":[{"id":"openai","name":"Local facts","protocol":"openai_responses","directory_provider":"openai","capabilities":{"catalog":"models_dev"}}]}}}`
	writeTaskFixture(t, filepath.Join(root, "local-facts", "plugin.json"), manifest)
	result, err := s.Profiles.Check(context.Background(), CheckProvider{ProviderTypeID: "local-facts/openai", Endpoint: "https://never-contacted.invalid/v1", Protocol: "openai_responses"})
	if err != nil || !result.Supported || result.Catalog == nil || len(result.Catalog.Models) == 0 {
		t.Fatal(result, err)
	}
}

func TestProviderEndpointPreviewOffersCorrectionWithoutPermittingMismatch(t *testing.T) {
	ctx := context.Background()
	s, _ := configurationService(t)
	value, err := s.Profiles.ResolveProviderEndpoint(ctx, CheckProvider{ProviderTypeID: customProviderType, Endpoint: "https://fixture.invalid/v1/responses", Protocol: "openai_chat"})
	if err != nil {
		t.Fatal(err)
	}
	resolved, ok := value.(modelprotocol.EndpointResolution)
	if !ok || resolved.DetectedProtocol != modelprotocol.OpenAIResponses || resolved.Endpoint != "https://fixture.invalid/v1/responses" {
		t.Fatal(value)
	}
	if _, err = s.Profiles.Save(ctx, SaveProfile{RequestID: "mismatched-format", Name: "Mismatch", Model: "fixture", ProviderTypeID: customProviderType, Endpoint: resolved.Endpoint, Protocol: "openai_chat", APIKey: "isolated-key", ModelSettings: &core.ModelSettings{}}); err == nil {
		t.Fatal("preview authorized a mismatched request format")
	}
}
