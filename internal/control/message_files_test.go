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

func awaitFiles(t *testing.T, s *Service, owner, message string, tasks ...string) []resources.MessageFile {
	t.Helper()
	task := ""
	if len(tasks) > 0 {
		task = tasks[0]
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		rows, err := s.MessageFiles(context.Background(), owner, task, message)
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
	other, err := s.CreateAssistant(ctx, CreateAssistant{RequestID: "foreign-files", Name: "Foreign"})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := s.MessageFiles(ctx, other.ID, "", "reply")
	if err != nil || len(foreign) != 0 {
		t.Fatal("foreign refs leaked")
	}
	if _, err = s.Resources.Get(ctx, other.ID, id); err == nil {
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
	for i, want := range []string{"ready", "not_found", "ready", "not_found", "ready", "ready"} {
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

func TestConfigurationAliasUsesCapturedRules(t *testing.T) {
	s, _ := configurationService(t)
	ctx := context.Background()
	a, err := s.CreateAssistant(ctx, CreateAssistant{RequestID: "config-alias", Name: "Configuration"})
	if err != nil {
		t.Fatal(err)
	}
	root, _ := s.ExecutionWorkspace(a.ID)
	directory := s.Registry.Directory(a.ID)
	foreign := t.TempDir()
	os.WriteFile(filepath.Join(foreign, "AGENTS.md"), []byte("foreign private configuration"), 0600)
	if err := os.Rename(directory, directory+"-original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(foreign, directory); err != nil {
		t.Fatal(err)
	}
	s.bindMessageFiles(a.ID, "", "", "config-link", "[rules]("+filepath.Join(directory, "AGENTS.md")+")", root)
	rows := awaitFiles(t, s, a.ID, "config-link")
	if rows[0].State != "ready" {
		t.Fatal("ordinary configuration alias was not shareable", rows)
	}
	legacy := rows[0].Source
	legacy.Rules = 0
	if err := s.Resources.BindMessage(ctx, a.ID, "main", "legacy-config", "[rules]("+filepath.Join(directory, "AGENTS.md")+")", legacy); err != nil {
		t.Fatal(err)
	}
	if got := awaitFiles(t, s, a.ID, "legacy-config")[0]; got.State != "forbidden" || got.Resource != nil {
		t.Fatal("legacy configuration alias gained access", got)
	}
}

// Direct completed-message fixtures use the same host binding callback.
func (s *Service) bindMessageFiles(owner, task, run, message, text, root string) {
	s.messageFileCompletion(owner, task, run, root)(message, text)
}
func TestMessageFileCompletionPinsExecutionRootBeforeModelRuns(t *testing.T) {
	s, _ := configurationService(t)
	a, err := s.CreateAssistant(context.Background(), CreateAssistant{RequestID: "pinned-execution", Name: "Pinned"})
	if err != nil {
		t.Fatal(err)
	}
	root, _ := s.ExecutionWorkspace(a.ID)
	completed := s.messageFileCompletion(a.ID, "", "actual-turn", root)
	foreign := t.TempDir()
	os.WriteFile(filepath.Join(foreign, "private.txt"), []byte("not in the execution root"), 0600)
	if err := os.Rename(root, root+"-original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(foreign, root); err != nil {
		t.Fatal(err)
	}
	completed("reply", "[file](private.txt)")
	rows := awaitFiles(t, s, a.ID, "reply")
	if rows[0].State != "forbidden" || rows[0].Source.Run != "actual-turn" {
		t.Fatal("execution root was rebound", rows)
	}
}

func TestMessageFileReadRejectsReplacementBetweenStatAndOpen(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "source.txt")
	os.WriteFile(path, []byte("before"), 0600)
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	info, err := root.Stat("source.txt")
	if err != nil {
		t.Fatal(err)
	}
	revision, err := taskFileRevision(info)
	if err != nil {
		t.Fatal(err)
	}
	replacement := filepath.Join(directory, "replacement")
	os.WriteFile(replacement, []byte("after!"), 0600)
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	if data, err := readBoundRoot(context.Background(), root, "source.txt", 1024, false, revision); err != errFileChanged || len(data) != 0 {
		t.Fatal("opened an uncaptured file revision", string(data), err)
	}
}
