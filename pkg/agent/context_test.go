package agent

import (
	"context"
	"encoding/json"
	"github.com/jjyr/pot/pkg/core"
	m "github.com/jjyr/pot/pkg/modelprotocol"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSectionsAppendChangesAndExplicitRevocations(t *testing.T) {
	sections := []core.ContextSection{{Key: "instructions", Text: " Rules\r\nnext "}, {Key: "reference", Text: "untrusted <x>"}}
	messages, err := SynchronizeSections(nil, sections)
	if err != nil || len(messages) != 2 {
		t.Fatal(messages, err)
	}
	messages = append(messages, m.TextMessage("user", "actual user request"))
	unchanged, err := SynchronizeSections(messages, []core.ContextSection{{Key: "reference", Text: "untrusted <x>"}, {Key: "instructions", Text: "Rules\nnext"}})
	if err != nil || len(unchanged) != 3 {
		t.Fatal(unchanged, err)
	}
	revoked, err := SynchronizeSections(unchanged, []core.ContextSection{{Key: "instructions", Text: "new rules"}})
	if err != nil || len(revoked) != 5 {
		t.Fatal(revoked, err)
	}
	if revoked[2].Text != "actual user request" || revoked[4].Kind.Active || !strings.Contains(revoked[4].Text, "no longer apply") {
		t.Fatal(revoked)
	}
	if _, err := SynchronizeSections(nil, []core.ContextSection{{Key: "bad\"key"}}); err == nil {
		t.Fatal("context injection accepted")
	}
}
func TestUsageAnchorOnlyCalibratesUnchangedNativePrefix(t *testing.T) {
	messages := []m.Message{m.TextMessage("user", strings.Repeat("history", 1000)), m.TextMessage("assistant", "done")}
	messages[1].Usage = &m.TokenUsage{Input: 100, Output: 20}
	route := "provider:model:openai_chat"
	messages[1].ReplayRoute = &route
	request := m.Request{Protocol: m.OpenAIChat, Model: "model", System: "system", Messages: messages, MaxOutputTokens: 10}
	anchor, err := captureAnchor(request, messages)
	if err != nil {
		t.Fatal(err)
	}
	a := &Agent{}
	estimate, err := a.estimate(request, anchor, true)
	if err != nil || estimate != 120 {
		t.Fatal(estimate, err)
	}
	request.Messages = append(request.Messages, m.TextMessage("user", "new guidance"))
	estimate, err = a.estimate(request, anchor, true)
	if err != nil || estimate != 123 {
		t.Fatal(estimate, err)
	}
	request.Messages[1].ReplayRoute = nil
	local, _ := a.estimate(request, nil, true)
	estimate, err = a.estimate(request, anchor, true)
	if err != nil || estimate != local {
		t.Fatal("stale route usage calibrated", estimate, local, err)
	}
}
func TestReconciliationOnlyUsesConfirmedResultsAndStripsImagePayloadFromText(t *testing.T) {
	id := "call"
	state := Checkpoint{Type: "context", Version: 2, Messages: []m.Message{}, Pending: []m.ToolCall{{ID: id, Name: "probe", Arguments: json.RawMessage(`{}`)}}, Started: &id, Receipts: []ToolReceipt{}, SteeringIDs: []string{}}
	raw, _ := json.Marshal(state)
	unchanged, err := ReconcileContext(raw, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	var restored Checkpoint
	_ = json.Unmarshal(unchanged, &restored)
	if restored.Started == nil || len(restored.Pending) != 1 {
		t.Fatal("unknown receipt treated as success")
	}
	result := json.RawMessage(`{"content":[{"type":"text","text":"observation"},{"type":"image","mimeType":"image/jpeg","data":"AQID","width":1280,"height":800}]}`)
	resolved, err := ReconcileContext(raw, map[string]json.RawMessage{id: result}, true)
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(resolved, &restored)
	if restored.Started != nil || len(restored.Pending) != 0 || len(restored.Messages) != 1 || len(restored.Messages[0].Images) != 1 || strings.Contains(restored.Messages[0].Text, "AQID") {
		t.Fatal(restored)
	}
	if _, err := ReconcileContext(raw, map[string]json.RawMessage{id: result}, false); err == nil {
		t.Fatal("image silently sent to text model")
	}
}
func TestSkillDiscoveryPrioritySelectionSymlinksAndMetadataOnly(t *testing.T) {
	root := t.TempDir()
	high, low := filepath.Join(root, "high"), filepath.Join(root, "low")
	write := func(base, name, description string) {
		t.Helper()
		dir := filepath.Join(base, name)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: "+name+"\ndescription: |\n  "+description+"\n  Next line\n---\nSECRET_BODY"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(high, "pdf", "Read <PDF> & tables")
	write(low, "pdf", "Shadowed")
	write(low, "builtin", "Reserved")
	if err := os.Symlink(high, filepath.Join(low, "linked")); err != nil {
		t.Fatal(err)
	}
	resources := core.SkillResources{Roots: []core.SkillRoot{{Path: high, Location: "/assigned/high"}, {Path: low, Location: "/assigned/low"}}, ReservedNames: []string{"builtin"}}
	catalog, err := DiscoverSkills(resources)
	if err != nil || len(catalog.Skills) != 1 || catalog.Skills[0].Location != "/assigned/high/pdf/SKILL.md" || len(catalog.Diagnostics) != 2 {
		t.Fatal(catalog, err)
	}
	prompt, err := FormatSkillsForPrompt(catalog.Skills)
	if err != nil || !strings.Contains(prompt, "&lt;PDF&gt; &amp; tables") || strings.Contains(prompt, "SECRET_BODY") || strings.Contains(prompt, high) {
		t.Fatal(prompt, err)
	}
	selection := []string{}
	resources.Selection = &selection
	catalog, err = DiscoverSkills(resources)
	if err != nil || len(catalog.Skills) != 0 {
		t.Fatal(catalog, err)
	}
	selection = []string{"missing"}
	if _, err := DiscoverSkills(resources); err == nil {
		t.Fatal("missing selected skill ignored")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	resources.Selection = nil
	if _, err := discoverSkills(ctx, resources); err == nil {
		t.Fatal("cancellation ignored")
	}
}
