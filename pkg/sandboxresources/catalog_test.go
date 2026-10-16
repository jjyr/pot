package sandboxresources

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"github.com/jjyr/pot/pkg/resource"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestOCIImageBindsVerifiedDirectory(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	store, err := resource.New(filepath.Join(t.TempDir(), "cache"), resource.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	tw := tar.NewWriter(gz)
	payload := []byte(`{"schemaVersion":2}`)
	if err = tw.WriteHeader(&tar.Header{Name: "index.json", Mode: 0644, Size: int64(len(payload))}); err != nil {
		t.Fatal(err)
	}
	tw.Write(payload)
	layout := []byte(`{"imageLayoutVersion":"1.0.0"}`)
	if err = tw.WriteHeader(&tar.Header{Name: "oci-layout", Mode: 0644, Size: int64(len(layout))}); err != nil {
		t.Fatal(err)
	}
	tw.Write(layout)
	tw.Close()
	gz.Close()
	makeResource := func(name string, data []byte, format resource.Format, unpacked uint64) resource.Resource {
		sum := sha256.Sum256(data)
		if err = os.WriteFile(filepath.Join(directory, name), data, 0600); err != nil {
			t.Fatal(err)
		}
		return resource.Resource{ReleaseTag: "fixture", Filename: name, SHA256: hex.EncodeToString(sum[:]), SizeBytes: uint64(len(data)), UnpackedBytes: unpacked, Format: format}
	}
	c := Catalog{SchemaVersion: 2, DefaultLinuxBundles: map[string]string{runtime.GOARCH: "linux"}, Resources: map[string]resource.Resource{"oci": makeResource("oci.tar.gz", archive.Bytes(), resource.OCITarGzip, uint64(len(payload)+len(layout)))}, Bundles: map[string]Bundle{"linux": {Image: "oci", ImageReference: "fixture:image", ManifestDigest: "sha256:" + strings.Repeat("a", 64), BootstrapVersion: "pot-executor-v2", OS: "linux", Architecture: runtime.GOARCH}}}
	prepared, err := c.Import(ctx, store, "", directory)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(prepared.Image)
	if err != nil || !info.IsDir() {
		t.Fatal(prepared.Image, err)
	}
	if prepared.Architecture != runtime.GOARCH || prepared.ManifestDigest != c.Bundles["linux"].ManifestDigest {
		t.Fatal("image identity lost", prepared)
	}
	for arch := range map[string]bool{"arm64": true, "amd64": true} {
		c.DefaultLinuxBundles = map[string]string{arch: "linux"}
		b := c.Bundles["linux"]
		b.Architecture = arch
		c.Bundles["linux"] = b
		if err := c.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	c.SchemaVersion = 1
	if err := c.Validate(); err == nil {
		t.Fatal("retired kernel/boot catalog accepted")
	}
}
