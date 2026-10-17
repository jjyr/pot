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
	"github.com/jjyr/pot/pkg/resource"
)

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
	bundle, err := resource.LoadPlugin("../../plugins/github")
	if err != nil {
		t.Fatal(err)
	}
	if err = bundle.ValidateInput("github", map[string]any{"args": []any{"pr", "list", "--repo", "owner/repo"}}); err != nil {
		t.Fatal(err)
	}
	if err = bundle.ValidateInput("github", map[string]any{"args": []any{17}}); err == nil {
		t.Fatal("nonstring argv accepted")
	}
	if bundle.Manifest.Contributes.Tools.Credentials["GH_TOKEN"].Service != "github" || bundle.Manifest.Contributes.Tools.SessionMode != "call" {
		t.Fatal("incorrect credential/lifecycle declaration")
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
