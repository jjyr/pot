package resourcecontrol

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/resource"
	"github.com/jjyr/pot/internal/sandbox"
	"github.com/jjyr/pot/internal/sandboxresources"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func fixtureData() string {
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	tw := tar.NewWriter(gz)
	for _, item := range [][2]string{{"index.json", `{"schemaVersion":2}`}, {"oci-layout", `{"imageLayoutVersion":"1.0.0"}`}} {
		_ = tw.WriteHeader(&tar.Header{Name: item[0], Mode: 0644, Size: int64(len(item[1]))})
		_, _ = tw.Write([]byte(item[1]))
	}
	_ = tw.Close()
	_ = gz.Close()
	return out.String()
}
func fixture(t *testing.T, transfer http.RoundTripper) (Options, func()) {
	t.Helper()
	root := t.TempDir()
	data := fixtureData()
	sum := sha256.Sum256([]byte(data))
	res := resource.Resource{ReleaseTag: "sandbox-go-resources-v0.1.0", Filename: "fixture.tar.gz", SHA256: hex.EncodeToString(sum[:]), SizeBytes: uint64(len(data)), UnpackedBytes: uint64(len(`{"schemaVersion":2}`) + len(`{"imageLayoutVersion":"1.0.0"}`)), Format: resource.OCITarGzip}
	b := sandboxresources.Bundle{Image: "asset", ImageReference: "fixture:immutable", ManifestDigest: "sha256:" + strings.Repeat("a", 64), BootstrapVersion: "pot-executor-v2", OS: "linux", Architecture: runtime.GOARCH}
	c := sandboxresources.Catalog{SchemaVersion: 2, DefaultLinuxBundles: map[string]string{runtime.GOARCH: "bundle"}, Resources: map[string]resource.Resource{"asset": res}, Bundles: map[string]sandboxresources.Bundle{"bundle": b, "other": b}}
	if transfer == nil {
		transfer = roundTrip(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(data)), ContentLength: int64(len(data))}, nil
		})
	}
	s, err := resource.New(filepath.Join(root, "cache"), resource.Options{Client: &http.Client{Transport: transfer}})
	if err != nil {
		t.Fatal(err)
	}
	options := Options{Root: filepath.Join(root, "control"), Catalog: c, Store: s, Bind: func(context.Context, string, sandbox.PreparedResources) error { return nil }}
	return options, func() {
		if err := s.Close(context.Background()); err != nil {
			t.Error(err)
		}
	}
}
func await(t *testing.T, m *Manager, id, state string) Request {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		r := m.entries[id].request
		changed := m.changed
		m.mu.Unlock()
		if r.State == state {
			return r
		}
		if r.State == "failed" && state != "failed" {
			t.Fatalf("unexpected failure: %s", r.Error)
		}
		select {
		case <-changed:
		case <-time.After(20 * time.Millisecond):
		}
	}
	t.Fatalf("request did not become %s", state)
	return Request{}
}
func TestConsentPinsSharedPreparationAndCachedReuse(t *testing.T) {
	var downloads atomic.Int32
	options, closeStore := fixture(t, roundTrip(func(r *http.Request) (*http.Response, error) {
		downloads.Add(1)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(fixtureData())), ContentLength: int64(len(fixtureData()))}, nil
	}))
	defer closeStore()
	var mu sync.Mutex
	bound := map[string]int{}
	options.Bind = func(_ context.Context, id string, p sandbox.PreparedResources) error {
		if p.BundleID != "bundle" || p.Image == "" {
			t.Error("unverified binding")
		}
		mu.Lock()
		bound[id]++
		mu.Unlock()
		return nil
	}
	m, err := Open(options)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close(context.Background())
	a, err := m.Request(context.Background(), "sandboxA", "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := m.Request(context.Background(), "sandboxB", "")
	if err != nil {
		t.Fatal(err)
	}
	status, err := m.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if downloads.Load() != 0 || a.State != "awaiting_consent" || status.Bundles[0].Ready {
		t.Fatal("status/request downloaded without consent")
	}
	if _, err = m.Request(context.Background(), "sandboxA", "other"); err == nil {
		t.Fatal("changed immutable pin")
	}
	waited := make(chan error, 1)
	go func() { waited <- m.Wait(context.Background(), "sandboxB", "") }()
	if _, err = m.Start(a.ID); err != nil {
		t.Fatal(err)
	}
	await(t, m, a.ID, "ready")
	await(t, m, b.ID, "ready")
	if err = <-waited; err != nil {
		t.Fatal(err)
	}
	if downloads.Load() != 1 {
		t.Fatalf("expected one digest transfer, got %d", downloads.Load())
	}
	c, err := m.Request(context.Background(), "sandboxC", "")
	if err != nil || c.State != "ready" || downloads.Load() != 1 {
		t.Fatalf("cached resources not reused: %+v %v", c, err)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, id := range []string{"sandboxA", "sandboxB", "sandboxC"} {
		if bound[id] != 1 {
			t.Fatalf("binding count %s = %d", id, bound[id])
		}
	}
}
func TestIndependentCancellationAndRestartDoesNotDownload(t *testing.T) {
	started := make(chan struct{}, 4)
	release := make(chan struct{})
	var downloads atomic.Int32
	options, closeStore := fixture(t, roundTrip(func(r *http.Request) (*http.Response, error) {
		downloads.Add(1)
		started <- struct{}{}
		select {
		case <-r.Context().Done():
			return nil, r.Context().Err()
		case <-release:
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(fixtureData())), ContentLength: int64(len(fixtureData()))}, nil
	}))
	defer closeStore()
	m, err := Open(options)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := m.Request(context.Background(), "sandboxA", "")
	b, _ := m.Request(context.Background(), "sandboxB", "")
	m.Start(a.ID)
	<-started
	m.Start(b.ID)
	// Ensure the second request has subscribed to the same Store transfer.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		p := m.entries[b.ID].request.Progress.Phase
		m.mu.Unlock()
		if p != "" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if _, err = m.Cancel(a.ID); err != nil {
		t.Fatal(err)
	}
	close(release)
	await(t, m, b.ID, "ready")
	await(t, m, a.ID, "cancelled")
	if downloads.Load() != 1 {
		t.Fatalf("cancelled another subscriber's transfer: %d", downloads.Load())
	}
	if err = m.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	m, err = Open(options)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close(context.Background())
	s, err := m.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Requests) != 2 || downloads.Load() != 1 {
		t.Fatal("restart replayed preparation")
	}
}
func TestOfflineImportAndInterruptedCheckpoint(t *testing.T) {
	options, closeStore := fixture(t, nil)
	defer closeStore()
	m, err := Open(options)
	if err != nil {
		t.Fatal(err)
	}
	r, _ := m.Request(context.Background(), "offlineSandbox", "")
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "fixture.tar.gz"), []byte(fixtureData()), 0600)
	if _, err = m.Import(r.ID, dir); err != nil {
		t.Fatal(err)
	}
	await(t, m, r.ID, "ready")
	// Simulate an accepted durable checkpoint left by an interrupted process.
	m.mu.Lock()
	e := m.entries[r.ID]
	e.request.State = "preparing"
	if err = m.saveLocked(e); err != nil {
		t.Fatal(err)
	}
	m.mu.Unlock()
	m.Close(context.Background())
	m, err = Open(options)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close(context.Background())
	await(t, m, r.ID, "interrupted")
	if err = m.Wait(context.Background(), "offlineSandbox", ""); err == nil || errors.Is(err, context.Canceled) {
		t.Fatal("restart silently resumed an operation")
	}
}
