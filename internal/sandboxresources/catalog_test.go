package sandboxresources

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"github.com/jjyr/pot/internal/resource"
	"os"
	"path/filepath"
	"testing"
)

func TestRawKernelBindsVerifiedFileAndOCIBindsVerifiedDirectory(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	store, err := resource.New(filepath.Join(t.TempDir(), "cache"), resource.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	kernel := []byte("test kernel")
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
	c := Catalog{SchemaVersion: 1, DefaultLinuxBundle: "linux", Resources: map[string]resource.Resource{"kernel": makeResource("kernel", kernel, resource.Raw, uint64(len(kernel))), "oci": makeResource("oci.tar.gz", archive.Bytes(), resource.OCITarGzip, uint64(len(payload)+len(layout)))}, Bundles: map[string]Bundle{"linux": {Kernel: "kernel", Boot: "oci", Image: "oci", ImageReference: "fixture:image", BootReference: "fixture:boot", BootstrapVersion: "pot-executor-v2", OS: "linux", Architecture: "arm64"}}}
	prepared, err := c.Import(ctx, store, "", directory)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(prepared.Kernel)
	if err != nil || !info.Mode().IsRegular() {
		t.Fatal("kernel bound a directory", prepared.Kernel, err)
	}
	for _, path := range []string{prepared.Boot, prepared.Image} {
		info, err = os.Stat(path)
		if err != nil || !info.IsDir() {
			t.Fatal(path, err)
		}
	}
	data, err := os.ReadFile(prepared.Kernel)
	if err != nil || !bytes.Equal(data, kernel) {
		t.Fatal(err)
	}
}
