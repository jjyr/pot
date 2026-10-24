package control

import (
	"context"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/resources"
)

func awaitFiles(t *testing.T, s *Service, owner, message string) []resources.MessageFile {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		rows, err := s.MessageFiles(context.Background(), owner, "", message)
		if err != nil {
			t.Fatal(err)
		}
		ready := len(rows) > 0
		for _, r := range rows {
			if r.State == "pending" || r.State == "resolving" {
				ready = false
			}
		}
		if ready {
			return rows
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("file references timed out")
	return nil
}
func TestMessageFilesSnapshotIsolationAndReplay(t *testing.T) {
	s, _ := configurationService(t)
	ctx := context.Background()
	a, err := s.CreateAssistant(ctx, CreateAssistant{RequestID: "files", Name: "Files"})
	if err != nil {
		t.Fatal(err)
	}
	root, _ := s.ExecutionWorkspace(a.ID)
	path := filepath.Join(root, "有 空格(一).md")
	os.WriteFile(path, []byte("original\nline two"), 0600)
	text := "[报告](<" + path + ">)\n\nSee [位置](<" + path + ":2>).\n\n[encoded](" + (&url.URL{Path: path}).EscapedPath() + ")\n\n`[bad](/etc/passwd)`"
	s.bindMessageFiles(a.ID, "", "", "reply", text, root)
	rows := awaitFiles(t, s, a.ID, "reply")
	if len(rows) != 3 || rows[0].State != "ready" || rows[1].State != "ready" || rows[2].State != "ready" || rows[0].Resource.ID != rows[1].Resource.ID || rows[0].Resource.ID != rows[2].Resource.ID {
		t.Fatalf("%+v", rows)
	}
	id := rows[0].Resource.ID
	os.WriteFile(path, []byte("later"), 0600)
	s.bindMessageFiles(a.ID, "", "", "reply", text, root)
	os.Remove(path)
	if err = s.RetryMessageFile(ctx, a.ID, "", "reply", rows[0].ID); err != nil {
		t.Fatal(err)
	}
	replay := awaitFiles(t, s, a.ID, "reply")
	if replay[0].Resource.ID != id {
		t.Fatal("replay reread source")
	}
	file, _, err := s.Resources.OpenFile(ctx, a.ID, id, rows[0].Resource.Entry)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	data, _ := io.ReadAll(file)
	if string(data) != "original\nline two" {
		t.Fatal("snapshot changed")
	}
	foreign, err := s.MessageFiles(ctx, "other", "", "reply")
	if err == nil && len(foreign) != 0 {
		t.Fatal("foreign refs leaked")
	}
	if _, err = s.Resources.Get(ctx, "other", id); err == nil {
		t.Fatal("foreign resource leaked")
	}
}
func TestMessageFilesRejectPathsAndRetry(t *testing.T) {
	s, _ := configurationService(t)
	ctx := context.Background()
	a, _ := s.CreateAssistant(ctx, CreateAssistant{RequestID: "files", Name: "Files"})
	root, _ := s.ExecutionWorkspace(a.ID)
	outside := filepath.Join(t.TempDir(), "private")
	os.WriteFile(outside, []byte("private"), 0600)
	os.Symlink(outside, filepath.Join(root, "escape"))
	ownConfig := filepath.Join(s.Registry.Directory(a.ID), "AGENTS.md")
	os.WriteFile(ownConfig, []byte("own rules"), 0600)
	text := "[绝对](" + outside + ")\n\n[逃逸](../private)\n\n[符号](escape)\n\n[缺少](missing.txt)\n\n[自身](" + ownConfig + ")\n\n[其他配置](" + filepath.Join(s.Registry.Directory(a.ID), "profile.json") + ")"
	s.bindMessageFiles(a.ID, "", "", "reply", text, root)
	rows := awaitFiles(t, s, a.ID, "reply")
	for i, want := range []string{"forbidden", "forbidden", "forbidden", "not_found", "ready", "forbidden"} {
		if rows[i].State != want {
			t.Fatalf("%d got %s want %s", i, rows[i].State, want)
		}
	}
	os.WriteFile(filepath.Join(root, "missing.txt"), []byte("now present"), 0600)
	if err := s.RetryMessageFile(ctx, a.ID, "", "reply", rows[3].ID); err != nil {
		t.Fatal(err)
	}
	rows = awaitFiles(t, s, a.ID, "reply")
	if rows[3].State != "ready" {
		t.Fatal(rows[3])
	}
	if err := s.RetryMessageFile(ctx, "other", "", "reply", rows[0].ID); err == nil {
		t.Fatal("foreign retry accepted")
	}
	// A node address is host provenance, never a hint to use a same-named local file.
	source := rows[3].Source
	source.Node = "offline-node"
	if err := s.Resources.BindMessage(ctx, a.ID, "main", "remote", "[x](missing.txt)", source); err != nil {
		t.Fatal(err)
	}
	if got := awaitFiles(t, s, a.ID, "remote")[0].State; got != "source_offline" {
		t.Fatal(got)
	}
}
func TestMessageHTMLDependenciesAndContentDetection(t *testing.T) {
	s, _ := configurationService(t)
	a, _ := s.CreateAssistant(context.Background(), CreateAssistant{RequestID: "html", Name: "HTML"})
	root, _ := s.ExecutionWorkspace(a.ID)
	for path, text := range map[string]string{"demo.html": `<link rel="stylesheet" href="style.css"><button onclick="this.textContent='done'">Try</button>`, "style.css": "body{color:red}", "bad.html": `<script src="https://example.com/x.js"></script>`, "missing.html": `<img src="no.png">`} {
		os.WriteFile(filepath.Join(root, path), []byte(text), 0600)
	}
	s.bindMessageFiles(a.ID, "", "", "reply", "[演示](demo.html)\n\n[坏](bad.html)\n\n[缺失](missing.html)", root)
	rows := awaitFiles(t, s, a.ID, "reply")
	if rows[0].State != "ready" || len(rows[0].Resource.Files) != 2 || rows[1].State != "unsupported_dependency" || rows[2].State != "not_found" {
		t.Fatal(rows)
	}
	output, err := resources.RenderHTML(*rows[0].Resource, func(path string) ([]byte, error) {
		f, _, e := s.Resources.OpenFile(context.Background(), a.ID, rows[0].Resource.ID, path)
		if e != nil {
			return nil, e
		}
		defer f.Close()
		return io.ReadAll(f)
	}, "token")
	if err != nil || !strings.Contains(string(output), "data:text/css;base64,") {
		t.Fatal(err, string(output))
	}
}

func TestMessageFilesDirectoryReplacementAndRegularFileLimits(t *testing.T) {
	s, _ := configurationService(t)
	ctx := context.Background()
	a, _ := s.CreateAssistant(ctx, CreateAssistant{RequestID: "bounds", Name: "Bounds"})
	root, _ := s.ExecutionWorkspace(a.ID)
	os.WriteFile(filepath.Join(root, "first.md"), []byte("first"), 0600)
	source := resources.Source{Node: s.options.RuntimeID, Owner: a.ID, Root: root}
	info, _ := os.Stat(root)
	source.RootID, _ = taskDirectoryIdentity(info)
	if err := os.Rename(root, root+"-old"); err != nil {
		t.Fatal(err)
	}
	os.Mkdir(root, 0700)
	os.WriteFile(filepath.Join(root, "first.md"), []byte("replacement"), 0600)
	if err := s.Resources.BindMessage(ctx, a.ID, "main", "swapped", "[file](first.md)", source); err != nil {
		t.Fatal(err)
	}
	if state := awaitFiles(t, s, a.ID, "swapped")[0].State; state != "forbidden" {
		t.Fatal(state)
	}
	large, _ := os.Create(filepath.Join(root, "large.bin"))
	large.Truncate(resources.FileLimit + 1)
	large.Close()
	s.bindMessageFiles(a.ID, "", "", "large", "[large](large.bin)\n\n[directory](.)", root)
	rows := awaitFiles(t, s, a.ID, "large")
	if rows[0].State != "too_large" || rows[1].State != "forbidden" {
		t.Fatal(rows)
	}
}
