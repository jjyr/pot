package resource

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func describe(data []byte, format Format, unpacked uint64) Resource {
	hash := sha256.Sum256(data)
	return Resource{ReleaseTag: "v1", Filename: "resource", SHA256: hex.EncodeToString(hash[:]), SizeBytes: uint64(len(data)), UnpackedBytes: unpacked, Format: format}
}
func store(t *testing.T, client *http.Client) *Store {
	t.Helper()
	s, err := New(filepath.Join(t.TempDir(), "cache"), Options{Client: client})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := s.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	return s
}
func TestImportRevalidatesBlobAndEveryContentFile(t *testing.T) {
	data := []byte("kernel")
	r := describe(data, Raw, uint64(len(data)))
	source := filepath.Join(t.TempDir(), "kernel")
	os.WriteFile(source, data, 0600)
	s := store(t, nil)
	v, err := s.Import(context.Background(), r, source)
	if err != nil {
		t.Fatal(err)
	}
	payload := filepath.Join(v.Path(), "payload")
	if err = os.WriteFile(payload, []byte("tamper"), 0600); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(s.root, r.SHA256, "complete.json"), []byte(`{"payload":"forged"}`), 0600)
	if _, _, err = s.Cached(context.Background(), r); err == nil {
		t.Fatal("completion marker authorized altered content")
	}
	os.WriteFile(payload, data, 0600)
	os.WriteFile(filepath.Join(v.Path(), "extra"), []byte("extra"), 0600)
	if _, _, err = s.Cached(context.Background(), r); err == nil {
		t.Fatal("extra cache file accepted")
	}
	os.Remove(filepath.Join(v.Path(), "extra"))
	os.Remove(payload)
	os.Symlink(source, payload)
	if _, _, err = s.Cached(context.Background(), r); err == nil {
		t.Fatal("cache symlink accepted")
	}
}

type archiveEntry struct {
	name, text string
	kind       byte
	link       string
}

func archiveBytes(t *testing.T, entries []archiveEntry) []byte {
	t.Helper()
	var output bytes.Buffer
	gz := gzip.NewWriter(&output)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		h := &tar.Header{Name: e.name, Mode: 0600, Typeflag: e.kind, Linkname: e.link}
		if e.kind == tar.TypeReg {
			h.Size = int64(len(e.text))
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Size > 0 {
			tw.Write([]byte(e.text))
		}
	}
	tw.Close()
	gz.Close()
	return output.Bytes()
}
func TestOCIExtractionRejectsPathsLinksDuplicatesAndSize(t *testing.T) {
	base := []archiveEntry{{"oci-layout", "{}", tar.TypeReg, ""}, {"index.json", "{}", tar.TypeReg, ""}}
	for _, test := range []struct {
		name     string
		extra    archiveEntry
		unpacked uint64
	}{{"parent", archiveEntry{"../escape", "x", tar.TypeReg, ""}, 5}, {"link", archiveEntry{"link", "", tar.TypeSymlink, "index.json"}, 4}, {"duplicate", archiveEntry{"index.json", "x", tar.TypeReg, ""}, 5}, {"size", archiveEntry{"large", "many bytes", tar.TypeReg, ""}, 5}} {
		t.Run(test.name, func(t *testing.T) {
			data := archiveBytes(t, append(append([]archiveEntry{}, base...), test.extra))
			r := describe(data, OCITarGzip, test.unpacked)
			source := filepath.Join(t.TempDir(), "archive")
			os.WriteFile(source, data, 0600)
			if _, err := store(t, nil).Import(context.Background(), r, source); err == nil {
				t.Fatal("unsafe OCI archive accepted")
			}
		})
	}
	data := archiveBytes(t, base)
	r := describe(data, OCITarGzip, 4)
	source := filepath.Join(t.TempDir(), "valid")
	os.WriteFile(source, data, 0600)
	s := store(t, nil)
	v, err := s.Import(context.Background(), r, source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(v.Path(), "index.json")); err != nil {
		t.Fatal(err)
	}
	combined := append(append([]byte{}, data...), data...)
	r = describe(combined, OCITarGzip, 4)
	source = filepath.Join(t.TempDir(), "double")
	os.WriteFile(source, combined, 0600)
	if _, err = s.Import(context.Background(), r, source); err == nil {
		t.Fatal("second gzip payload accepted")
	}
}
func TestCoalescedTransferPerCallerCancellation(t *testing.T) {
	data := bytes.Repeat([]byte("k"), 128*1024)
	r := describe(data, Raw, uint64(len(data)))
	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if calls.Add(1) == 1 {
			close(started)
		}
		select {
		case <-req.Context().Done():
			return
		case <-release:
		}
		w.Write(data)
	}))
	defer server.Close()
	s := store(t, server.Client())
	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() { _, err := s.Prepare(ctx, r, server.URL, nil); first <- err }()
	<-started
	joined := make(chan struct{})
	second := make(chan error, 1)
	go func() {
		_, err := s.Prepare(context.Background(), r, server.URL, func(Progress) {
			select {
			case <-joined:
			default:
				close(joined)
			}
		})
		second <- err
	}()
	<-joined
	cancel()
	if err := <-first; err != context.Canceled {
		t.Fatal(err)
	}
	close(release)
	if err := <-second; err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("duplicate transfer", calls.Load())
	}
	if _, err := s.Prepare(context.Background(), r, server.URL, nil); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("cached resource downloaded again")
	}
}
func TestDownloadDigestMismatchDoesNotPublish(t *testing.T) {
	data := []byte("trusted")
	r := describe(data, Raw, uint64(len(data)))
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "changed") }))
	defer server.Close()
	s := store(t, server.Client())
	if _, err := s.Prepare(context.Background(), r, server.URL, nil); err == nil {
		t.Fatal("bad digest published")
	}
	entries, _ := os.ReadDir(s.root)
	if len(entries) != 0 {
		t.Fatal("failed resource left cache state", entries)
	}
}
