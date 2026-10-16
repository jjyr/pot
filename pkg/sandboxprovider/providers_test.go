package sandboxprovider

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jjyr/pot/pkg/sandbox"
)

func TestUnavailableProviderInspectionHasNoSideEffects(t *testing.T) {
	root := t.TempDir()
	instance := sandbox.Instance{Root: root, ID: "probe"}
	for _, provider := range []sandbox.Provider{NewAppleVM(AppleVMConfig{})} {

		status, err := provider.Lifecycle(context.Background(), instance, "status")
		if err != nil || status.Phase != "unavailable" {
			t.Fatalf("%s: %+v %v", provider.Descriptor().ID, status, err)
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("inspection created files: %v %v", entries, err)
	}
}

func TestEnrolledMacOSManifestRequiresPrivateCompleteBundle(t *testing.T) {
	bundle := filepath.Join(t.TempDir(), "macos")
	if err := os.Mkdir(bundle, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range macOSFiles {
		if err := os.WriteFile(filepath.Join(bundle, name), []byte("test"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	manifest := MacOSManifest{SchemaVersion: 1, Provider: sandbox.AppleVM, GuestBuild: "26A", XcodeVersion: "26", Bootstrap: "pot-macos-executor-v3", GuestIP: "192.168.65.3", MACAddress: "02:00:00:00:00:03"}
	if err := privateJSON(filepath.Join(bundle, "manifest.json"), manifest); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadMacOSManifest(bundle); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(bundle, "executor-token"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadMacOSManifest(bundle); err == nil {
		t.Fatal("public credential accepted")
	}
	if err := os.Chmod(filepath.Join(bundle, "executor-token"), 0600); err != nil {
		t.Fatal(err)
	}
	manifest.GuestIP = "8.8.8.8"
	if err := privateJSON(filepath.Join(bundle, "manifest.json"), manifest); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadMacOSManifest(bundle); err == nil {
		t.Fatal("public guest address accepted")
	}
	manifest.GuestIP = "192.168.65.3"
	manifest.MACAddress = "00:11:22:33:44:55"
	if err := privateJSON(filepath.Join(bundle, "manifest.json"), manifest); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadMacOSManifest(bundle); err == nil {
		t.Fatal("globally administered MAC accepted")
	}
}

func TestCredentialReadRejectsSymlinksAndOversizedFiles(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "secret")
	if err := os.WriteFile(file, []byte("credential"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := privateFile(file, 4); err == nil {
		t.Fatal("large file accepted")
	}
	if err := os.Symlink(file, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := privateFile(filepath.Join(root, "link"), 64); err == nil {
		t.Fatal("symlink accepted")
	}
	if got, err := privateFile(file, 64); err != nil || string(got) != "credential" {
		t.Fatalf("%q %v", got, err)
	}
}

func TestNativeGenerationRetainsUint64Precision(t *testing.T) {
	status, err := nativeStatus(map[string]any{"phase": "running", "generation": json.Number("18446744073709551615")})
	if err != nil || status.Generation != ^uint64(0) {
		t.Fatalf("%+v %v", status, err)
	}
	if _, err := nativeStatus(map[string]any{"phase": "running", "generation": float64(10)}); err == nil {
		t.Fatal("inexact wire generation accepted")
	}
}
