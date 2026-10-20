package plugin

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jjyr/pot/pkg/core"
)

func TestModelProviderIdentityAndToolProjection(t *testing.T) {
	p := mixedPackage(t, t.TempDir())
	p.Manifest.Contributes.ModelProviders = &core.PluginModelProviders{Providers: []core.ModelProviderDefinition{{ID: "test", Name: "Test", Protocol: "openai_chat", Capabilities: core.ModelProviderCapabilities{Catalog: "plugin"}}}, Adapter: &core.ModelProviderAdapter{Runtime: "node", Execution: "control", Entrypoint: "reader.mjs", Files: []string{"reader.mjs"}}}
	b, err := p.ToolBundle()
	if err != nil {
		t.Fatal(err)
	}
	if b.Manifest.Contributes.ModelProviders != nil {
		t.Fatal("reader entered Task bundle")
	}
	forged := b
	forged.Manifest.Contributes.ModelProviders = p.Manifest.Contributes.ModelProviders
	forged.Digest, _ = forged.Hash()
	if forged.Validate() == nil {
		t.Fatal("reader accepted from forged Task bundle")
	}
	if _, err = p.ModelProviderIdentity(); err == nil {
		t.Fatal("missing adapter accepted")
	}
	path := filepath.Join(p.Root, "reader.mjs")
	if err = os.WriteFile(path, []byte("export async function read(){}"), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := p.ModelProviderIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("export async function read(){return {}}"), 0600); err != nil {
		t.Fatal(err)
	}
	after, err := p.ModelProviderIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if before.Digest == after.Digest {
		t.Fatal("adapter edit did not fence identity")
	}
	p.Manifest.Contributes.ModelProviders.Adapter = nil
	p.Manifest.Contributes.ModelProviders.Providers[0].Capabilities.Catalog = "http"
	if _, err = p.ModelProviderIdentity(); err != nil {
		t.Fatal("fileless declaration unavailable", err)
	}
}
