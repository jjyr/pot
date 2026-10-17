package sandboxprovider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/plugin"
	"github.com/jjyr/pot/pkg/sandbox"
)

type fixtureProvider struct {
	config     Config
	id         string
	mu         sync.Mutex
	generation uint64
}

func (p *fixtureProvider) Descriptor() sandbox.Descriptor {
	return sandbox.Descriptor{ID: p.id, HostOS: runtime.GOOS, OS: "fixture", Engine: "fixture", Capabilities: sandbox.Capabilities{Shell: true, Files: true, Desktop: true, SharedExecution: true, HostMappings: true}}
}
func (p *fixtureProvider) DefaultResources() sandbox.Resources {
	return sandbox.Resources{CPUs: 1, MemoryGiB: 1}
}
func (p *fixtureProvider) ValidateResources(_ sandbox.Instance, r sandbox.Resources) error {
	if r.CPUs != 1 {
		return errors.New("fixture CPU limit")
	}
	return nil
}
func (p *fixtureProvider) Workspace(i sandbox.Instance) string {
	return filepath.Join(i.Root, "instances", i.ID, "workspace")
}
func (p *fixtureProvider) GuestWorkspace() string               { return "/workspace" }
func (p *fixtureProvider) CleanupPolicy() sandbox.CleanupPolicy { return sandbox.ProcessContainment }
func (p *fixtureProvider) BindResources(_ context.Context, _ sandbox.Instance, r sandbox.PreparedResources) error {
	if r.Provider != p.id {
		return errors.New("wrong resource provider")
	}
	return nil
}
func (p *fixtureProvider) Lifecycle(_ context.Context, _ sandbox.Instance, method string) (sandbox.Status, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if method == "start" {
		p.generation++
	}
	return sandbox.Status{Phase: "stopped", Generation: p.generation}, nil
}
func (p *fixtureProvider) Health(context.Context, sandbox.Instance) (bool, error) { return true, nil }
func (p *fixtureProvider) Request(ctx context.Context, i sandbox.Instance, op string, input json.RawMessage, _ time.Duration) (json.RawMessage, error) {
	switch op {
	case "echo":
		return input, nil
	case "environment":
		_, present := os.LookupEnv("OPENAI_API_KEY")
		return json.Marshal(map[string]bool{"credential_inherited": present})
	case "cleanup_unknown":
		return nil, errors.Join(core.ErrCleanupUnconfirmed, errors.New("cleanup remains unknown"))
	case "wait":
		<-ctx.Done()
		return nil, ctx.Err()
	case "effect_then_exit":
		path := filepath.Join(i.Root, "effects")
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			return nil, err
		}
		_, _ = f.WriteString("effect\n")
		_ = f.Close()
		os.Exit(17)
	}
	return nil, errors.New("unsupported fixture request")
}
func (p *fixtureProvider) Shutdown(context.Context) error { return nil }
func (p *fixtureProvider) DesktopCapabilities() sandbox.DesktopCapabilities {
	return sandbox.DesktopCapabilities{Viewing: true, Protocol: "rfb-3.8"}
}
func (p *fixtureProvider) PrepareDesktop(context.Context, sandbox.Instance) error { return nil }
func (p *fixtureProvider) DesktopMetrics(context.Context, sandbox.Instance) (json.RawMessage, error) {
	return json.RawMessage(`{"width":800}`), nil
}
func (p *fixtureProvider) DesktopBrowser(context.Context, sandbox.Instance) error { return nil }
func (p *fixtureProvider) OpenDesktop(context.Context, sandbox.Instance) (io.ReadWriteCloser, error) {
	a, b := net.Pipe()
	go func() { defer b.Close(); _, _ = io.Copy(b, b) }()
	return a, nil
}
func TestProviderProcess(t *testing.T) {
	position := slices.Index(os.Args, "--provider-fixture")
	if position < 0 {
		return
	}
	err := Serve(func(c Config) (sandbox.Provider, error) {
		return &fixtureProvider{config: c, id: os.Args[position+1]}, nil
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}
func fixturePackage(t *testing.T, directory, id string) string {
	t.Helper()
	packageRoot := filepath.Join(directory, id)
	if err := os.MkdirAll(packageRoot, 0700); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nexec '" + strings.ReplaceAll(executable, "'", "'\\''") + "' -test.run='^TestProviderProcess$' -- --provider-fixture '" + id + "'\n"
	if err = os.WriteFile(filepath.Join(packageRoot, "provider"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	writeManifest(t, packageRoot, core.PluginManifest{SchemaVersion: 2, ID: id, Version: "1.0.0", Contributes: core.PluginContributions{SandboxProviders: []core.PluginSandboxProvider{{ID: id, HostOS: []string{runtime.GOOS}, Executable: "provider", DefaultShared: true}}}})
	return packageRoot
}
func writeManifest(t *testing.T, directory string, m core.PluginManifest) {
	t.Helper()
	body, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(directory, "plugin.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
}
func registryFixture(t *testing.T) (Registry, Config) {
	t.Helper()
	directory := t.TempDir()
	fixturePackage(t, directory, "fixture")
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	config := Config{Root: root}
	registry, err := loadFixture(context.Background(), directory, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, p := range registry.Providers {
			if err := p.Shutdown(ctx); err != nil {
				t.Error(err)
			}
		}
	})
	return registry, config
}
func TestDiscoveryRPCAndDesktopPreserveProviderContracts(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "fixture-secret")
	registry, config := registryFixture(t)
	p := registry.Providers[0]
	ctx := context.Background()
	i := sandbox.Instance{Root: config.Root, ID: "shared"}
	if registry.DefaultProvider != "fixture" || len(registry.Catalog.Bundles) != 0 || p.Workspace(i) != filepath.Join(config.Root, "instances/shared/workspace") {
		t.Fatal(registry, p.Workspace(i))
	}
	entries, err := os.ReadDir(config.Root)
	if err != nil || len(entries) != 0 {
		t.Fatal("discovery changed guest state", entries, err)
	}
	if err = p.ValidateResources(i, p.DefaultResources()); err != nil {
		t.Fatal(err)
	}
	if p.ValidateResources(i, sandbox.Resources{CPUs: 2}) == nil {
		t.Fatal("resource validation was bypassed")
	}
	if err = p.BindResources(ctx, i, sandbox.PreparedResources{Provider: "wrong"}); err == nil {
		t.Fatal("provider validation was bypassed")
	}
	for _, op := range []string{"echo", "environment"} {
		out, err := p.Request(ctx, i, op, json.RawMessage(`{"message":"hello"}`), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		expected := `{"message":"hello"}`
		if op == "environment" {
			expected = `{"credential_inherited":false}`
		}
		if string(out) != expected {
			t.Fatal(string(out))
		}
	}
	if _, err = p.Request(ctx, i, "cleanup_unknown", json.RawMessage(`{}`), time.Second); !errors.Is(err, core.ErrCleanupUnconfirmed) {
		t.Fatal("cleanup uncertainty lost", err)
	}
	state, err := p.Lifecycle(ctx, i, "start")
	if err != nil || state.Generation != 1 {
		t.Fatal(state, err)
	}
	desktop := p.(sandbox.DesktopProvider)
	if err = desktop.PrepareDesktop(ctx, i); err != nil {
		t.Fatal(err)
	}
	metrics, err := desktop.DesktopMetrics(ctx, i)
	if err != nil || string(metrics) != `{"width":800}` {
		t.Fatal(string(metrics), err)
	}
	stream, err := desktop.OpenDesktop(ctx, i)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	done := make(chan error, 1)
	go func() {
		_, err := stream.Write([]byte("RFB fixture"))
		if err == nil {
			body := make([]byte, 11)
			_, err = io.ReadFull(stream, body)
			if string(body) != "RFB fixture" {
				err = fmt.Errorf("desktop bytes changed: %q", body)
			}
		}
		done <- err
	}()
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		stream.Close()
		t.Fatal("desktop stream stalled")
	}
}
func TestLostAcknowledgementIsNeverReplayedOrRestarted(t *testing.T) {
	directory := t.TempDir()
	fixturePackage(t, directory, "fixture")
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	registry, err := loadFixture(context.Background(), directory, Config{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	p := registry.Providers[0]
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	i := sandbox.Instance{Root: root, ID: "shared"}
	for range 2 {
		if _, err = p.Request(ctx, i, "effect_then_exit", json.RawMessage(`{}`), time.Second); !errors.Is(err, core.ErrCleanupUnconfirmed) {
			t.Fatal("lost acknowledgement treated as safe", err)
		}
	}
	data, err := os.ReadFile(filepath.Join(root, "effects"))
	if err != nil || string(data) != "effect\n" {
		t.Fatal("effect replayed", string(data), err)
	}
	if err = p.Shutdown(ctx); !errors.Is(err, core.ErrCleanupUnconfirmed) {
		t.Fatal("dead provider cleanup confirmed", err)
	}
}
func TestCancellationLeavesProcessAvailableForInspection(t *testing.T) {
	registry, config := registryFixture(t)
	p := registry.Providers[0]
	i := sandbox.Instance{Root: config.Root, ID: "shared"}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := p.Request(ctx, i, "wait", json.RawMessage(`{}`), time.Second); !errors.Is(err, core.ErrCleanupUnconfirmed) {
		t.Fatal(err)
	}
	ready, err := p.Health(context.Background(), i)
	if err != nil || !ready {
		t.Fatal("cancellation destroyed provider", ready, err)
	}
}
func TestDiscoveryRejectsAmbiguousDefaultsTraversalAndProtocolMismatch(t *testing.T) {
	for _, kind := range []string{"ambiguous", "traversal", "version", "identity", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			directory := t.TempDir()
			packageRoot := fixturePackage(t, directory, "fixture")
			body, err := os.ReadFile(filepath.Join(packageRoot, "plugin.json"))
			if err != nil {
				t.Fatal(err)
			}
			var m core.PluginManifest
			if err = json.Unmarshal(body, &m); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "ambiguous":
				fixturePackage(t, directory, "second")
			case "traversal":
				m.Contributes.SandboxProviders[0].Executable = "../provider"
			case "version":
				m.SchemaVersion = 99
			case "identity":
				m.Contributes.SandboxProviders[0].ID = "different"
			case "symlink":
				if err = os.Symlink(filepath.Join(packageRoot, "provider"), filepath.Join(packageRoot, "link")); err != nil {
					t.Fatal(err)
				}
				m.Contributes.SandboxProviders[0].Executable = "link"
			}
			writeManifest(t, packageRoot, m)
			root, _ := filepath.EvalSymlinks(t.TempDir())
			if _, err = loadFixture(context.Background(), directory, Config{Root: root}); err == nil {
				t.Fatal("invalid plugin accepted")
			}
		})
	}
}

func TestParentChannelLossExitsWithoutFabricatingShutdownAcknowledgement(t *testing.T) {
	directory := t.TempDir()
	fixturePackage(t, directory, "fixture")
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	registry, err := loadFixture(context.Background(), directory, Config{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	p := registry.Providers[0].(*Client)
	if err = p.stdin.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.done:
	case <-time.After(4 * time.Second):
		_ = p.command.Process.Kill()
		t.Fatal("orphan provider remained alive")
	}
	if err = p.Shutdown(context.Background()); !errors.Is(err, core.ErrCleanupUnconfirmed) {
		t.Fatal("missing shutdown acknowledgement accepted", err)
	}
}

func loadFixture(ctx context.Context, directory string, config Config) (Registry, error) {
	packages, err := plugin.Discover(directory)
	if err != nil {
		return Registry{}, err
	}
	return Load(ctx, packages, config)
}

func TestCatalogOnlyPluginContributesToInstalledProvider(t *testing.T) {
	directory := t.TempDir()
	fixturePackage(t, directory, "podman")
	imageRoot := filepath.Join(directory, "image")
	if err := os.MkdirAll(imageRoot, 0700); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("../../plugins/podman/catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(imageRoot, "catalog.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	writeManifest(t, imageRoot, core.PluginManifest{SchemaVersion: 2, ID: "image", Version: "1", Contributes: core.PluginContributions{Resources: []core.PluginResourceCatalog{{Provider: "podman", Catalog: "catalog.json"}}}})
	packages, err := plugin.Discover(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(packages) != 2 {
		t.Fatal(packages)
	}
	for _, p := range packages {
		if p.Manifest.ID == "image" {
			if _, err = p.ToolBundle(); err == nil {
				t.Fatal("catalog-only plugin became executable tool")
			}
		}
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	registry, err := Load(context.Background(), packages, Config{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := registry.Providers[0].Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	if len(registry.Providers) != 1 || len(registry.Catalog.Bundles) == 0 {
		t.Fatal(registry)
	}
	// A catalog does not supply or implicitly install the referenced provider.
	if err = os.RemoveAll(filepath.Join(directory, "podman")); err != nil {
		t.Fatal(err)
	}
	packages, err = plugin.Discover(directory)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Load(context.Background(), packages, Config{Root: root}); err == nil {
		t.Fatal("catalog silently installed missing Provider")
	}
}
