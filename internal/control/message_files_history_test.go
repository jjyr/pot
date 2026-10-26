package control

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/jjyr/pot/internal/resources"
	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/storage"
)

// Insert a historical queue row without running today's binding callback. All
// paths, databases and authentication material belong to this test's fixture.
func storedMessageFile(t *testing.T, s *Service, message, href string, source json.RawMessage, state string, saved *resources.Resource) resources.MessageFile {
	t.Helper()
	var src resources.Source
	if err := json.Unmarshal(source, &src); err != nil {
		t.Fatal(err)
	}
	text := "[file](" + href + ")"
	link := resources.ParseLinks(text)[0]
	revision, _ := core.HashJSON(text)
	id, _ := core.HashJSON([]any{src.Owner, "main", message, revision, link.Start, link.End, href})
	raw := ""
	if saved != nil {
		data, err := json.Marshal(saved)
		if err != nil {
			t.Fatal(err)
		}
		raw = string(data)
	}
	db, err := storage.OpenDatabase(filepath.Join(s.options.Root, "control.sqlite"), storage.ControlDatabase)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`INSERT INTO message_files(owner,conversation,message,revision,ref,start,end,href,label,block,source,state,resource) VALUES(?,'main',?,?,?,?,?,?,?,?,?,?,?)`, src.Owner, message, revision, id, link.Start, link.End, href, link.Label, link.Block, string(source), state, raw)
	if err != nil {
		t.Fatal(err)
	}
	return resources.MessageFile{ID: id, Message: message, Revision: revision, Link: link, Source: src, ReceiptSource: source, State: state, Resource: saved}
}

func historicalSource(t *testing.T, source resources.Source, rules any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	if rules == nil {
		return raw
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	fields["rules"] = rules
	raw, err = json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestMessageFilesUnifiedPolicyAndRetry(t *testing.T) {
	s, _ := configurationService(t)
	ctx := t.Context()
	a, _ := s.CreateAssistant(ctx, CreateAssistant{RequestID: "history-files", Name: "Files"})
	root, _ := s.ExecutionWorkspace(a.ID)
	info, _ := os.Stat(root)
	rootID, _ := taskDirectoryIdentity(info)
	source := resources.Source{Node: s.options.RuntimeID, Owner: a.ID, Root: root, RootID: rootID}
	outside := filepath.Join(t.TempDir(), "external.txt")
	writeTaskFixture(t, outside, "current bytes, unavailable in history")
	key := filepath.Join(t.TempDir(), "synthetic-key")
	writeTaskFixture(t, key, "synthetic authentication material")
	s.options.Keys = secrets.FileKey{Path: key}
	for i, rules := range []any{nil, 1, 2, 999, "obsolete"} {
		raw := historicalSource(t, source, rules)
		message := fmt.Sprintf("history-%d", i)
		failed := storedMessageFile(t, s, message, outside, raw, "forbidden", nil)
		// History reads and completion recovery do not rescan or rebind failures.
		s.bindMessageFiles(a.ID, "", "new-run", message, "[file]("+outside+")", root)
		before := awaitFiles(t, s, a.ID, message)[0]
		if before.State != "forbidden" || before.Source.Run != "" {
			t.Fatal("history was retried or rebound", before)
		}
		want := fmt.Sprintf("current bytes for retry %d", i)
		writeTaskFixture(t, outside, want)
		if err := s.RetryMessageFile(ctx, a.ID, "", message, failed.ID); err != nil {
			t.Fatal(err)
		}
		got := awaitFiles(t, s, a.ID, message)[0]
		if got.State != "ready" {
			t.Fatal("historical rules selected reading authority", rules, got)
		}
		assertSnapshotBytes(t, s, got, want)
		denied := storedMessageFile(t, s, message+"-auth", key, raw, "not_found", nil)
		if err := s.RetryMessageFile(ctx, a.ID, "", denied.Message, denied.ID); err != nil {
			t.Fatal(err)
		}
		if got := awaitFiles(t, s, a.ID, denied.Message)[0]; got.State != "forbidden" || got.Resource != nil {
			t.Fatal("historical source bypassed authentication exception", got)
		}
	}
	for _, kind := range []string{"node", "task", "root"} {
		invalid := source
		want := "forbidden"
		switch kind {
		case "node":
			invalid.Node = "unavailable-node"
			want = "source_offline"
		case "task":
			invalid.Task = "unowned-task"
		case "root":
			invalid.RootID = "different-directory"
		}
		ref := storedMessageFile(t, s, "identity-"+kind, outside, historicalSource(t, invalid, 1), "pending", nil)
		if got := awaitFiles(t, s, a.ID, ref.Message)[0]; got.State != want {
			t.Fatal("unified rules bypassed source identity", kind, got)
		}
	}
}

func assertSnapshotBytes(t *testing.T, s *Service, ref resources.MessageFile, want string) {
	t.Helper()
	file, _, err := s.Resources.OpenFile(t.Context(), ref.Source.Owner, ref.Resource.ID, ref.Resource.Entry)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil || string(data) != want {
		t.Fatal("snapshot bytes changed", string(data), err)
	}
}

func TestMessageFilesRecoverHistoricalReceiptsWithoutReadingSource(t *testing.T) {
	s, _ := configurationService(t)
	ctx := t.Context()
	a, _ := s.CreateAssistant(ctx, CreateAssistant{RequestID: "receipt-files", Name: "Files"})
	root, _ := s.ExecutionWorkspace(a.ID)
	info, _ := os.Stat(root)
	rootID, _ := taskDirectoryIdentity(info)
	source := resources.Source{Node: s.options.RuntimeID, Owner: a.ID, Root: root, RootID: rootID}
	source.Config = filepath.Join(s.Registry.Directory(a.ID), "AGENTS.md")
	configInfo, _ := os.Stat(filepath.Dir(source.Config))
	source.ConfigID, _ = taskDirectoryIdentity(configInfo)
	// Neither the entry nor the old own-AGENTS directory remains readable.
	if err := os.Rename(filepath.Dir(source.Config), filepath.Dir(source.Config)+"-gone"); err != nil {
		t.Fatal(err)
	}
	for i, test := range []struct {
		rules            any
		confined, config bool
	}{
		{nil, true, false}, {1, true, false},
		{nil, true, true}, {1, true, true},
		{2, false, false}, {nil, false, false},
	} {
		raw := historicalSource(t, source, test.rules)
		message := fmt.Sprintf("receipt-%d", i)
		path := filepath.Join(root, "deleted.txt")
		entry, base := "deleted.txt", root
		if test.config {
			path, entry, base = source.Config, "AGENTS.md", filepath.Dir(source.Config)
		}
		revision, _ := core.HashJSON("[file](" + path + ")")
		identity := []any{raw, false, path, message, revision}
		if test.confined {
			identity = []any{raw, base, entry, message, revision}
		}
		hash, _ := core.HashJSON(identity)
		saved, err := s.Resources.Register(ctx, a.ID, "message-file-"+hash, hash, entry, entry, []string{entry}, [][]byte{[]byte("immutable historical bytes")})
		if err != nil {
			t.Fatal(err)
		}
		ref := storedMessageFile(t, s, message, path, raw, "ready", &saved)
		if err := s.RetryMessageFile(ctx, a.ID, "", message, ref.ID); err != nil {
			t.Fatal(err)
		}
		if got := awaitFiles(t, s, a.ID, message)[0]; got.State != "ready" || got.Resource.ID != saved.ID {
			t.Fatal("ready snapshot lost", got)
		}
		// Simulate a crash after registration but before the queue state committed.
		if err := s.Resources.FileState(ctx, ref.ID, "resolving", nil); err != nil {
			t.Fatal(err)
		}
		got := awaitFiles(t, s, a.ID, message)[0]
		if got.State != "ready" || got.Resource.ID != saved.ID {
			t.Fatal("receipt recovery touched deleted source", got)
		}
		assertSnapshotBytes(t, s, got, "immutable historical bytes")
	}
}
