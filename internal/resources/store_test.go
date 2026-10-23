package resources

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

func TestDisplayLabelsAndDownloadFilenamesAreIndependent(t *testing.T) {
	root := t.TempDir()
	s, err := Open(filepath.Join(root, "originals"), filepath.Join(root, "control.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	labels := []string{"Pot 通信记录 · 修订 02（A/B 局部对照）", `A\B`, strings.Repeat("中文", 200), strings.Repeat("长显示名称/", 2000), "NUL.html", "..", ".hidden", "line\x00\r\nbreak"}
	paths := []string{"demo/index.html", "demo/ui.css"}
	contents := [][]byte{[]byte("<link rel=stylesheet href=ui.css>"), []byte("body{color:red}")}
	for index, label := range labels {
		for _, bundle := range []bool{false, true} {
			count := 1
			if bundle {
				count = 2
			}
			request := fmt.Sprintf("label-%d-%t", index, bundle)
			r, err := s.Register(ctx, "alice", request, request, label, paths[0], paths[:count], contents[:count])
			if err != nil {
				t.Fatal(err)
			}
			stored, err := s.Get(ctx, "alice", r.ID)
			if err != nil || stored.Name != label || stored.Entry != paths[0] || stored.Files[0].Path != paths[0] {
				t.Fatal("display label or original path changed", err)
			}
			for _, mode := range []string{"preview", "download"} {
				if err := (Presentation{Resource: stored, Mode: mode}).Validate(); err != nil {
					t.Fatal("presentation rejected display label", err)
				}
			}
			if r.Download.Path != core.DownloadFilename(r.Download.Path) || len(r.Download.Path) > 255 {
				t.Fatal("unsafe download filename", r.Download.Path)
			}
			file, _, err := s.OpenFile(ctx, "alice", r.ID, "@download")
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(file)
			file.Close()
			if err != nil {
				t.Fatal(err)
			}
			if !bundle {
				if r.Download.Path != "index.html" || !bytes.Equal(data, contents[0]) {
					t.Fatal("single-file download changed with display label")
				}
				continue
			}
			if !strings.HasSuffix(r.Download.Path, ".zip") || r.Files[1].Path != paths[1] {
				t.Fatal("bundle filename or asset path changed")
			}
			if label == ".hidden" && r.Download.Path != ".hidden.zip" {
				t.Fatal("dotfile label lost its stem")
			}
			archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
			if err != nil || len(archive.File) != len(paths) {
				t.Fatal("invalid bundle", err)
			}
			for i, entry := range archive.File {
				reader, err := entry.Open()
				if err != nil {
					t.Fatal(err)
				}
				original, err := io.ReadAll(reader)
				reader.Close()
				if err != nil || entry.Name != paths[i] || !bytes.Equal(original, contents[i]) {
					t.Fatal("archive entry changed", err)
				}
			}
		}
	}
	for i, name := range []string{"CON.txt", "notes?.txt", strings.Repeat("中", 100) + ".txt"} {
		r, err := s.Register(ctx, "alice", fmt.Sprint("filename-", i), "hash", "Display A/B", "dir/"+name, []string{"dir/" + name}, [][]byte{[]byte("original")})
		if err != nil || r.Download.Path != core.DownloadFilename(name) || r.Entry != "dir/"+name {
			t.Fatal("single-file filename conversion", err)
		}
	}
}

func TestResourceOriginalsOwnershipReceiptAndRestart(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	open := func() *Store {
		s, err := Open(filepath.Join(root, "originals"), filepath.Join(root, "control.sqlite"))
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	s := open()
	raw := []byte("original contents")
	saved, err := s.Register(ctx, "alice", "call", "hash", "report.txt", "report.txt", []string{"report.txt"}, [][]byte{raw})
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.Register(ctx, "alice", "call", "hash", "report.txt", "report.txt", []string{"report.txt"}, [][]byte{[]byte("changed")})
	if err != nil || saved.ID != again.ID {
		t.Fatal("registration replay changed resource", err)
	}
	if _, err = s.Receipt(ctx, "alice", "call", "other"); err == nil {
		t.Fatal("conflicting input accepted")
	}
	if _, err = s.Get(ctx, "bob", saved.ID); err == nil {
		t.Fatal("cross owner read")
	}
	if _, _, err = s.OpenFile(ctx, "alice", saved.ID, "../report.txt"); err == nil {
		t.Fatal("path traversal")
	}
	s.Close()
	s = open()
	defer s.Close()
	chunk, err := s.Chunk(ctx, "alice", saved.ID, "", 3)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := base64.StdEncoding.DecodeString(chunk.Data)
	if string(data) != string(raw[3:]) || chunk.Revision != saved.Files[0].Digest {
		t.Fatal("original changed after reopen")
	}
	reader := &Reader{File: saved.Files[0], ReadChunk: func(offset uint64) (chunkOut protocol.FileChunk, err error) {
		return s.Chunk(ctx, "alice", saved.ID, "", offset)
	}}
	if _, err = reader.Seek(-8, io.SeekEnd); err != nil {
		t.Fatal(err)
	}
	data, err = io.ReadAll(reader)
	if err != nil || string(data) != "contents" {
		t.Fatal("range seek", string(data), err)
	}
	if _, err = s.Register(ctx, "alice", "bad", "hash", "bad.html", "bad.html", []string{"bad.html", "../escape"}, [][]byte{[]byte("hello"), raw}); err == nil {
		t.Fatal("invalid bundle accepted")
	}
}

func TestHTMLBundleUsesIsolatedSelfContainedAssets(t *testing.T) {
	resource := Resource{MIMEType: "text/html", Entry: "demo/index.html", Files: []File{{Path: "demo/index.html"}, {Path: "demo/ui.js", MIMEType: "text/javascript"}, {Path: "demo/ui.css", MIMEType: "text/css"}, {Path: "demo/dot.png", MIMEType: "image/png"}}}
	originals := map[string][]byte{"demo/index.html": []byte(`<html><head><base href="https://evil.test"><link rel="stylesheet" href="ui.css"><meta http-equiv="refresh" content="0;url=https://evil.test"></head><body><script src="ui.js"></script><img src="https://evil.test/pixel"><style>div{background:url(dot.png)}</style></body></html>`), "demo/ui.js": []byte("document.body.dataset.ready='yes'"), "demo/ui.css": []byte("body{background:url(dot.png)}"), "demo/dot.png": []byte("image")}
	output, err := RenderHTML(resource, func(path string) ([]byte, error) { return originals[path], nil }, "token")
	if err != nil {
		t.Fatal(err)
	}
	text := string(output)
	if strings.Contains(text, "evil.test") || strings.Contains(text, "<base") || !strings.Contains(text, "data:text/javascript;base64,") || !strings.Contains(text, "data:text/css;base64,") || !strings.Contains(text, "Object.defineProperty(window,'pot'") {
		t.Fatal("invalid isolated document", text)
	}
	if !strings.Contains(HTMLPolicy, "sandbox allow-scripts") || strings.Contains(HTMLPolicy, "allow-same-origin") {
		t.Fatal("missing opaque origin")
	}
}

func TestVoiceSignaturesAndInvalidRaster(t *testing.T) {
	for _, sample := range []struct {
		path string
		data []byte
		want string
	}{{"voice.mp3", []byte{0xff, 0xfb, 0x90, 0}, "audio/mpeg"}, {"voice.aac", []byte{0xff, 0xf1, 0x50, 0}, "audio/aac"}, {"voice.flac", []byte("fLaC"), "audio/flac"}} {
		if got := DetectType(sample.path, sample.data); got != sample.want {
			t.Fatal(sample.path, got)
		}
	}
	root := t.TempDir()
	store, err := Open(filepath.Join(root, "resources"), filepath.Join(root, "control.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.Register(context.Background(), "alice", "bad-image", "hash", "image.png", "image.png", []string{"image.png"}, [][]byte{{137, 80, 78, 71, 13, 10, 26, 10}}); err == nil {
		t.Fatal("invalid raster signature accepted without valid dimensions")
	}
}
