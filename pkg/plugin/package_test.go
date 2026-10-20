package plugin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jjyr/pot/pkg/core"
)

func mixedPackage(t *testing.T, root string) Package {
	t.Helper()
	m := core.PluginManifest{SchemaVersion: 2, ID: "mixed", Version: "1", Contributes: core.PluginContributions{
		Background: &core.PluginBackground{Runtime: "node", Execution: "control", Entrypoint: "service.mjs", Files: []string{"service.mjs"}},
		Tools:      &core.PluginTools{Runtime: "node", Execution: "runtime", Entrypoint: "index.mjs", Files: []string{"index.mjs"}, Requirements: []string{}, Tools: []json.RawMessage{json.RawMessage(`{"name":"echo","description":"echo","inputSchema":{"type":"object","properties":{},"additionalProperties":false}}`)}},
	}}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "plugin.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "index.mjs"), []byte("export const tool = true;"), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestMixedPackageToolProjectionCannotCarryLocalCapabilities(t *testing.T) {
	p := mixedPackage(t, t.TempDir())
	// Background files deliberately do not exist: selecting the tool must read
	// only its declared tool files.
	b, err := p.ToolBundle()
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Files) != 1 || b.Files["index.mjs"] == "" || b.Manifest.Contributes.Background != nil {
		t.Fatal("local capabilities leaked", b)
	}
	if err = b.Validate(); err != nil {
		t.Fatal(err)
	}
	forgedCapability, _ := p.ToolBundle()
	forgedCapability.Manifest.Contributes.Background = p.Manifest.Contributes.Background
	forgedCapability.Digest, _ = forgedCapability.Hash()
	if forgedCapability.Validate() == nil {
		t.Fatal("local background capability entered transferable bundle")
	}
	forged, _ := p.ToolBundle()
	forged.Files["extra.mjs"] = "unselected payload"
	forged.Digest, _ = forged.Hash()
	if forged.Validate() == nil {
		t.Fatal("extra tool payload accepted")
	}

}

func TestCommonDiscoveryRejectsDuplicateIdentityAndUnsafeToolFiles(t *testing.T) {
	root := t.TempDir()
	p := mixedPackage(t, filepath.Join(root, "first"))
	mixedPackage(t, filepath.Join(root, "second"))
	if _, err := Discover(root); err == nil {
		t.Fatal("duplicate plugin ID accepted")
	}
	if err := os.Remove(filepath.Join(p.Root, "index.mjs")); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "private")
	if err := os.WriteFile(outside, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(p.Root, "index.mjs")); err != nil {
		t.Fatal(err)
	}
	if _, err := p.ToolBundle(); err == nil {
		t.Fatal("symlink tool payload accepted")
	}
	if _, err := p.ReadFile("../private", 65536); err == nil {
		t.Fatal("package traversal accepted")
	}
}

func TestUnsupportedAndLegacyCapabilitiesFailExplicitly(t *testing.T) {
	for _, manifest := range []string{
		`{"schema_version":1,"id":"old","version":"1","contributes":{"tools":{}}}`,
		`{"schema_version":2,"id":"empty","version":"1","contributes":{}}`,
		`{"schema_version":2,"id":"mcp","version":"1","contributes":{"mcp":{}}}`,
		`{"schema_version":2,"id":"bad","version":"1","contributes":{"sandbox_providers":[{"id":"test","host_os":["darwin"],"executable":"../provider","default_shared":false}]}}`,
	} {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "plugin.json"), []byte(manifest), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(root); err == nil {
			t.Fatal("unsupported manifest accepted", manifest)
		}
	}
}
