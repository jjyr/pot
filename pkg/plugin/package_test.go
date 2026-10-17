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
		Tools:            &core.PluginTools{Runtime: "node", Execution: "host_or_sandbox", Entrypoint: "index.mjs", Files: []string{"index.mjs"}, Requirements: []string{}, Tools: []json.RawMessage{json.RawMessage(`{"name":"echo","description":"echo","inputSchema":{"type":"object","properties":{},"additionalProperties":false}}`)}},
		SandboxProviders: []core.PluginSandboxProvider{{ID: "fixture.provider", HostOS: []string{"darwin", "linux"}, Executable: "bin/provider"}},
		Resources:        []core.PluginResourceCatalog{{Provider: "fixture.provider", Catalog: "catalog.json"}},
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
	// Provider and catalog files deliberately do not exist: selecting the tool
	// must neither read those files nor execute the local Provider capability.
	b, err := p.ToolBundle()
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Files) != 1 || b.Files["index.mjs"] == "" || len(b.Manifest.Contributes.SandboxProviders) != 0 || len(b.Manifest.Contributes.Resources) != 0 {
		t.Fatal("local capabilities leaked", b)
	}
	if err = b.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"provider", "catalog", "extra-file"} {
		t.Run(kind, func(t *testing.T) {
			forged, err := p.ToolBundle()
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "provider":
				forged.Manifest.Contributes.SandboxProviders = p.Manifest.Contributes.SandboxProviders
			case "catalog":
				forged.Manifest.Contributes.Resources = p.Manifest.Contributes.Resources
			case "extra-file":
				forged.Files["bin/provider"] = "native payload"
			}
			forged.Digest, err = forged.Hash()
			if err != nil {
				t.Fatal(err)
			}
			if forged.Validate() == nil {
				t.Fatal("forged execution bundle accepted")
			}
		})
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
