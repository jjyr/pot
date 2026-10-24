package control

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/storage"
)

func TestAssistantFilesAreAuthoritativeAndNotifyUI(t *testing.T) {
	t.Parallel()
	s, _ := configurationService(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	a, err := s.CreateAssistant(ctx, CreateAssistant{RequestID: "file-assistant"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.ToLower(strings.ReplaceAll(a.Name, " ", "-")) != a.ID || a.Name == a.ID {
		t.Fatal("initial display name must title-case both slug words", a.Name, a.ID)
	}
	for _, worker := range []bool{false, true} {
		for _, tool := range s.productTools(worker) {
			if tool.Name == "update_agent_profile" || tool.Name == "read_agent_instructions" || tool.Name == "update_agent_instructions" {
				t.Fatal("retired model tool remains advertised", tool.Name)
			}
		}
	}
	conversation, err := s.Conversation(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	before, err := conversation.Instructions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sub := s.Changes.Subscribe()
	defer sub.Close()
	baseline, err := sub.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	directory := s.Registry.Directory(a.ID)
	data, _ := json.Marshal(storage.AssistantProfile{SchemaVersion: 1, Name: "Edited in a file"})
	if err := os.WriteFile(filepath.Join(directory, "profile.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "AGENTS.md"), []byte("Use the current file rules."), 0600); err != nil {
		t.Fatal(err)
	}
	for {
		versions, err := sub.Next(ctx)
		if err != nil {
			t.Fatal("external edits did not notify the UI", err)
		}
		if versions["assistants"] > baseline["assistants"] && versions["instructions"] > baseline["instructions"] {
			break
		}
	}
	definitions, err := s.Registry.Definitions(ctx)
	if err != nil || len(definitions) != 1 || definitions[0].ID != a.ID || definitions[0].Name != "Edited in a file" || definitions[0].Avatar != a.Avatar {
		t.Fatal("directory did not read authoritative files", err)
	}
	if _, err := s.Registry.Rename(ctx, a.ID, "Stale UI draft", a.Revision); err == nil {
		t.Fatal("UI overwrote an external profile edit")
	}
	if _, err := conversation.SaveInstructions(ctx, "stale rules", before.Revision); err == nil {
		t.Fatal("UI overwrote external rule edits")
	}
	rules, _ := conversation.Instructions(ctx)
	execution, err := s.prepareExecution(ctx, a.ID, core.RuntimeTarget(s.options.RuntimeID), false, rules.Text)
	if err != nil || !strings.Contains(execution.Instructions, "Use the current file rules.") {
		t.Fatal("next execution did not reload AGENTS.md", err)
	}
}

func TestAssistantFileStampDetectsAtomicReplacementWithPreservedMetadata(t *testing.T) {
	t.Parallel()
	s := &Service{options: Options{Root: t.TempDir()}}
	directory := filepath.Join(s.options.Root, "assistants", "amber-otter")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "AGENTS.md")
	if err := os.WriteFile(path, []byte("first"), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	before := s.assistantFileStamp()
	if err := os.WriteFile(path+".next", []byte("other"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path+".next", info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path+".next", path); err != nil {
		t.Fatal(err)
	}
	if s.assistantFileStamp() == before {
		t.Fatal("replacement with the same length and modification time was missed")
	}
}
