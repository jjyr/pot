package sandboxresources

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/jjyr/pot/pkg/resource"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func fixture(t *testing.T) (Catalog, string) {
	t.Helper()
	directory := t.TempDir()
	data := []byte("opaque provider disk image")
	sum := sha256.Sum256(data)
	if err := os.WriteFile(filepath.Join(directory, "disk.raw"), data, 0600); err != nil {
		t.Fatal(err)
	}
	r := resource.Resource{ReleaseTag: "fixture", Filename: "disk.raw", SHA256: hex.EncodeToString(sum[:]), SizeBytes: uint64(len(data)), UnpackedBytes: uint64(len(data)), Format: resource.Raw}
	b := Bundle{Provider: "alternate", Architecture: runtime.GOARCH, Repository: "https://example.test", Resources: map[string]string{"disk": "asset"}, Metadata: json.RawMessage(`{"format":"custom-vm","bootstrap":"custom-v9"}`)}
	return Catalog{SchemaVersion: 3, Defaults: map[string]map[string]string{"alternate": {runtime.GOARCH: "vm"}}, Resources: map[string]resource.Resource{"asset": r}, Bundles: map[string]Bundle{"vm": b}}, directory
}
func TestProviderOwnsImageFormatAndBootstrap(t *testing.T) {
	c, directory := fixture(t)
	ctx := context.Background()
	store, err := resource.New(filepath.Join(t.TempDir(), "cache"), resource.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	prepared, err := c.Import(ctx, store, c.DefaultBundle("alternate"), directory)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Provider != "alternate" || string(prepared.Metadata) != string(c.Bundles["vm"].Metadata) || prepared.Architecture != runtime.GOARCH {
		t.Fatal(prepared)
	}
	if _, err = os.Stat(filepath.Join(prepared.Paths["disk"], "payload")); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Cached(ctx, store, ""); err == nil {
		t.Fatal("implicit cross-provider default accepted")
	}
	c.SchemaVersion = 2
	if c.Validate() == nil {
		t.Fatal("old image-specific catalog accepted")
	}
}
func TestCatalogRejectsCrossProviderDefaultsMissingResourcesAndCollisions(t *testing.T) {
	for _, kind := range []string{"default", "resource", "repository"} {
		t.Run(kind, func(t *testing.T) {
			c, _ := fixture(t)
			switch kind {
			case "default":
				c.Defaults["wrong"] = map[string]string{runtime.GOARCH: "vm"}
			case "resource":
				delete(c.Resources, "asset")
			case "repository":
				b := c.Bundles["vm"]
				b.Repository = "http://example.test"
				c.Bundles["vm"] = b
			}
			if c.Validate() == nil {
				t.Fatal("invalid catalog accepted")
			}
		})
	}
	c, _ := fixture(t)
	merged := Empty()
	if err := merged.Merge(c, "wrong"); err == nil {
		t.Fatal("cross-provider bundle accepted")
	}
	if err := merged.Merge(c, "alternate"); err != nil {
		t.Fatal(err)
	}
	if merged.Merge(c, "alternate") == nil {
		t.Fatal("duplicate resource identities accepted")
	}
}
