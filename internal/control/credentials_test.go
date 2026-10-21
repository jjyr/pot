package control

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jjyr/pot/pkg/integrations"
	"github.com/jjyr/pot/pkg/modelprotocol"
	"github.com/jjyr/pot/pkg/plugin"
)

func TestGitHubLoginAvailableWithoutDeploymentConfiguration(t *testing.T) {
	t.Setenv("POT_GITHUB_CLIENT_ID", "")
	t.Setenv("POT_GITHUB_CLIENT_SECRET", "")
	s := taskResourceService(t)
	items, err := s.Integrations.List(context.Background())
	if err != nil || len(items) != 1 || items[0].ID != "github" || !items[0].LoginAvailable || items[0].Status != "disconnected" {
		t.Fatal("built-in GitHub login unavailable", items, err)
	}
}

func TestCredentialCommandEnvironmentAndNonsecretReceipts(t *testing.T) {
	d, execution, _ := localDispatcher(t)
	d.Credentials = fixedCredentials("injected-fixture-token")
	call := modelprotocol.ToolCall{ID: "credential-command", Name: "exec_command", Arguments: json.RawMessage(`{"cmd":"test -n \"$GH_TOKEN\" && printf injected","credentials":{"GH_TOKEN":{"service":"github"}}}`)}
	value, err := d.Dispatch(context.Background(), execution, call, nil)
	if err != nil || !bytes.Contains(value, []byte("injected")) {
		t.Fatal(string(value), err)
	}
	for _, suffix := range []string{"", "-wal"} {
		raw, _ := os.ReadFile(filepath.Join(execution.Workspace, "operations.sqlite") + suffix)
		if bytes.Contains(raw, []byte("injected-fixture-token")) {
			t.Fatal("credential entered receipt")
		}
	}
	call.ID = "without-credentials"
	call.Arguments = json.RawMessage(`{"cmd":"test -z \"$GH_TOKEN\" && printf isolated","login":false}`)
	value, err = d.Dispatch(context.Background(), execution, call, nil)
	if err != nil || !bytes.Contains(value, []byte("isolated")) {
		t.Fatal("credential leaked to next command", string(value), err)
	}
}
func TestBundledGitHubPluginContract(t *testing.T) {
	p, err := plugin.Load("../../plugins/github")
	if err != nil {
		t.Fatal(err)
	}
	if p.Manifest.Contributes.Tools != nil || p.Manifest.Contributes.MCP == nil {
		t.Fatal("GitHub must expose real Control-local MCP")
	}
	if _, err = p.MCPIdentity(); err != nil {
		t.Fatal(err)
	}
	if _, err = p.ToolBundle(); err == nil {
		t.Fatal("Control MCP became a Runtime tool bundle")
	}
	if p.Manifest.Contributes.MCP.Credentials["GH_TOKEN"].Service != "github" {
		t.Fatal("incorrect credential declaration")
	}
}

// fixedCredentials tests the dispatch port without coupling it to account storage.
type fixedCredentials string

func (f fixedCredentials) Credential(context.Context, string, string, bool) (string, error) {
	return string(f), nil
}
func (f fixedCredentials) Forget(string) {}
func configureIntegration(t *testing.T, s *Service, token string) {
	t.Helper()
	ctx := context.Background()
	raw, _ := json.Marshal(integrations.Credential{AccessToken: token})
	ref := "integration/test"
	if err := s.integrationSecrets.Put(ctx, ref, raw); err != nil {
		t.Fatal(err)
	}
	if _, err := s.integrationAccounts.Save(ctx, integrations.Account{Service: "github", Identity: integrations.Identity{ID: "1", Username: "test"}, SecretRef: ref}, 0); err != nil {
		t.Fatal(err)
	}
}
