package sandboxprovider

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jjyr/pot/pkg/sandbox"
)

func testOCI(t *testing.T) (imageResources, string, string) {
	t.Helper()
	root := podmanTestRoot(t)
	if err := os.MkdirAll(filepath.Join(root, "blobs/sha256"), 0700); err != nil {
		t.Fatal(err)
	}
	blob := func(value []byte, mediaType string) ociDescriptor {
		hash := sha256.Sum256(value)
		digest := "sha256:" + hex.EncodeToString(hash[:])
		if err := os.WriteFile(filepath.Join(root, "blobs/sha256", digest[7:]), value, 0600); err != nil {
			t.Fatal(err)
		}
		return ociDescriptor{MediaType: mediaType, Digest: digest, Size: int64(len(value))}
	}
	config, _ := json.Marshal(map[string]any{"os": "linux", "architecture": runtime.GOARCH, "config": map[string]any{"Labels": map[string]string{"dev.pot.bootstrap": "pot-executor-v2"}}})
	configuration := blob(config, "application/vnd.oci.image.config.v1+json")
	layer := blob([]byte("verified fixture layer"), "application/vnd.oci.image.layer.v1.tar")
	manifest, _ := json.Marshal(map[string]any{"schemaVersion": 2, "config": configuration, "layers": []ociDescriptor{layer}})
	descriptor := blob(manifest, "application/vnd.oci.image.manifest.v1+json")
	reference := "localhost/pot-qualification:immutable"
	descriptor.Annotations = map[string]string{"org.opencontainers.image.ref.name": reference}
	index, _ := json.Marshal(map[string]any{"schemaVersion": 2, "mediaType": "application/vnd.oci.image.index.v1+json", "manifests": []ociDescriptor{descriptor}})
	if err := os.WriteFile(filepath.Join(root, "index.json"), index, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "oci-layout"), []byte(`{"imageLayoutVersion":"1.0.0"}`), 0600); err != nil {
		t.Fatal(err)
	}
	return imageResources{BundleID: "qualification", Image: root, ImageReference: reference, ManifestDigest: descriptor.Digest, Architecture: runtime.GOARCH}, configuration.Digest, filepath.Join(root, "blobs/sha256", layer.Digest[7:])
}
func TestPodmanBindVerifiesGraphWithoutMachineOrEngineSideEffects(t *testing.T) {
	resources, _, layer := testOCI(t)
	root := podmanTestRoot(t)
	p := NewPodman(PodmanConfig{RuntimeRoot: root, Binary: filepath.Join(root, "must-not-run")})
	p.hostOS = "darwin"
	i := sandbox.Instance{Root: root, ID: "shared"}
	if err := p.BindResources(context.Background(), i, resources.prepared()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "podman-machine")); !os.IsNotExist(err) {
		t.Fatal("cached binding initialized a machine", err)
	}
	if _, err := os.Stat(filepath.Join(i.Directory(), "podman.json")); !os.IsNotExist(err) {
		t.Fatal("binding accepted a container", err)
	}
	if err := os.WriteFile(layer, []byte("different contents"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := p.BindResources(context.Background(), i, resources.prepared()); err == nil {
		t.Fatal("corrupt OCI layer accepted")
	}
}
func TestPodmanBindingRejectsWrongReferenceManifestArchitectureAndSymlinks(t *testing.T) {
	for _, name := range []string{"reference", "manifest", "architecture", "symlink"} {
		t.Run(name, func(t *testing.T) {
			resource, _, layer := testOCI(t)
			switch name {
			case "reference":
				resource.ImageReference = "localhost/other:mutable"
			case "manifest":
				resource.ManifestDigest = "sha256:" + strings.Repeat("0", 64)
			case "architecture":
				if runtime.GOARCH == "arm64" {
					resource.Architecture = "amd64"
				} else {
					resource.Architecture = "arm64"
				}
			case "symlink":
				copy := layer + "-outside"
				if err := os.Rename(layer, copy); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(copy, layer); err != nil {
					t.Fatal(err)
				}
			}
			p := NewPodman(PodmanConfig{})
			if err := p.BindResources(context.Background(), sandbox.Instance{Root: podmanTestRoot(t), ID: "shared"}, resource.prepared()); err == nil {
				t.Fatal("invalid prepared OCI binding accepted")
			}
		})
	}
}
func TestPodmanImageProcessHelper(t *testing.T) {
	if os.Getenv("POT_PODMAN_IMAGE_HELPER") != "1" {
		return
	}
	sep := 0
	for i, arg := range os.Args {
		if arg == "--" {
			sep = i + 1
			break
		}
	}
	args := os.Args[sep:]
	state := os.Getenv("POT_PODMAN_IMAGE_STATE")
	config := os.Getenv("POT_PODMAN_IMAGE_CONFIG")
	if len(args) > 1 && args[1] == "load" {
		archive := tar.NewReader(os.Stdin)
		var names []string
		for {
			header, err := archive.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				os.Exit(3)
			}
			names = append(names, header.Name)
			if _, err = io.Copy(io.Discard, archive); err != nil {
				os.Exit(4)
			}
		}
		body, _ := json.Marshal(names)
		os.WriteFile(state, body, 0600)
	} else if len(args) > 2 && args[1] == "image" && args[2] == "inspect" {
		if _, err := os.Stat(state); err != nil {
			os.Exit(125)
		}
		json.NewEncoder(os.Stdout).Encode([]any{map[string]any{"Id": strings.TrimPrefix(config, "sha256:"), "Architecture": runtime.GOARCH, "Labels": map[string]string{"dev.pot.bootstrap": "pot-executor-v2"}}})
	} else {
		os.Exit(5)
	}
	os.Exit(0)
}
func TestPodmanImportsOnlyVerifiedOCIGraphOverStdinAndPinsConfiguration(t *testing.T) {
	resource, config, _ := testOCI(t)
	root := podmanTestRoot(t)
	p := NewPodman(PodmanConfig{})
	p.hostOS = "linux"
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	p.config.Binary = filepath.Join(root, "podman")
	script := "#!/bin/sh\nexec '" + strings.ReplaceAll(executable, "'", "'\\''") + "' -test.run=^TestPodmanImageProcessHelper$ -- \"$@\"\n"
	if err = os.WriteFile(p.config.Binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(root, "imported.json")
	t.Setenv("POT_PODMAN_IMAGE_HELPER", "1")
	t.Setenv("POT_PODMAN_IMAGE_STATE", state)
	t.Setenv("POT_PODMAN_IMAGE_CONFIG", config)
	i := sandbox.Instance{Root: root, ID: "shared"}
	if err = p.BindResources(context.Background(), i, resource.prepared()); err != nil {
		t.Fatal(err)
	}
	// A file outside the verified graph must never be sent to the engine.
	if err = os.WriteFile(filepath.Join(resource.Image, "unverified-owner-file"), []byte("do not load"), 0600); err != nil {
		t.Fatal(err)
	}
	actual, err := p.preparedImage(context.Background(), i)
	if err != nil || actual != config[7:] {
		t.Fatalf("%s %v", actual, err)
	}
	raw, err := os.ReadFile(state)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	if err = json.Unmarshal(raw, &names); err != nil {
		t.Fatal(err)
	}
	if len(names) != 5 {
		t.Fatalf("unexpected OCI archive: %v", names)
	}
	for _, name := range names {
		if name == "unverified-owner-file" || filepath.IsAbs(name) || strings.Contains(name, "..") {
			t.Fatalf("unverified or escaping archive member: %s", name)
		}
	}
	before := string(raw)
	actual, err = p.preparedImage(context.Background(), i)
	if err != nil || actual != config[7:] {
		t.Fatal(actual, err)
	}
	after, _ := os.ReadFile(state)
	if string(after) != before {
		t.Fatal("already verified image reimported")
	}
}
func TestPodmanBindingCannotReplaceAnAcceptedContainerImage(t *testing.T) {
	resource, _, _ := testOCI(t)
	root := podmanTestRoot(t)
	i := sandbox.Instance{Root: root, ID: "shared"}
	p := NewPodman(PodmanConfig{})
	if err := privateJSON(filepath.Join(i.Directory(), "podman.json"), podmanInstance{Name: "pot-original", Image: strings.Repeat("0", 64)}); err != nil {
		t.Fatal(err)
	}
	if err := p.BindResources(context.Background(), i, resource.prepared()); err == nil {
		t.Fatal("accepted image replaced by new resource binding")
	}
	if _, err := os.Stat(filepath.Join(i.Directory(), "prepared-resources.json")); !os.IsNotExist(err) {
		t.Fatal("rejected image binding persisted", err)
	}
}
