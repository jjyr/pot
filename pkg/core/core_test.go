package core

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestStrictTargetsAndAssignmentBinding(t *testing.T) {
	target := RuntimeTarget("remote")
	data, _ := json.Marshal(target)
	var restored ExecutionTarget
	if err := DecodeJSON(data, &restored); err != nil || restored != target {
		t.Fatalf("target %v %v", restored, err)
	}
	for _, data := range []string{`{"runtime_id":"a","target":{"kind":"host","sandbox_id":""}}`, `{"runtime_id":"a","target":{"kind":"sandbox"}}`, `{"runtime_id":"a","target":{"kind":"remote"}}`, `{"runtime_id":"a","runtime_id":"b","target":{"kind":"host"}}`, `{"runtime_id":"a","target":{"kind":"host"},"extra":1}`} {
		if err := DecodeJSON([]byte(data), &restored); err == nil {
			t.Fatal(data)
		}
	}
	assignment := AssignmentOnTarget("owner", "task", target)
	if err := assignment.ValidateBinding("owner", "task", target); err != nil {
		t.Fatal(err)
	}
	if err := assignment.ValidateBinding("owner", "task", RuntimeTarget("other")); err == nil {
		t.Fatal("Runtime identity substituted")
	}
	if err := assignment.ValidateBinding("other", "task", target); err == nil {
		t.Fatal("owner substituted")
	}
}
func TestStrictDefaultsNullAndCanonicalBytes(t *testing.T) {
	var s AgentSettings
	if err := DecodeJSON([]byte(`{"shell":{"allow_login_shell":false}}`), &s); err != nil {
		t.Fatal(err)
	}
	if s.Shell.AllowLoginShell || s.Shell.Executable != "/bin/bash" || s.ContextWindow != 32768 {
		t.Fatal(s)
	}
	for _, data := range []string{`{"context_window":null}`, `{"context_window":1,"Context_Window":2}`, `{"shell":{"typo":true}}`, `{} {}`, `{"shell":null}`} {
		if err := DecodeJSON([]byte(data), &s); err == nil {
			t.Fatal(data)
		}
	}
	a, err := CanonicalJSON(json.RawMessage(`{"z":1.0,"nested":{"b":0.25,"a":"<中文>"},"integer":18446744073709551615}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := CanonicalJSON(json.RawMessage(`{"integer":18446744073709551615,"nested":{"a":"<中文>","b":2.5e-1},"z":1}`))
	if err != nil || string(a) != string(b) {
		t.Fatalf("canonical mismatch %s %s %v", a, b, err)
	}
	if strings.Contains(string(a), `\u003c`) {
		t.Fatal(string(a))
	}
}
func TestCatalogMergeAndResolvedChecksum(t *testing.T) {
	catalog := ModelCatalog{SchemaVersion: 1, Default: json.RawMessage(`{"model_messages":{"instructions_template":"base","tools":{"browser":{"description":"base"}}}}`), Models: map[string]map[string]json.RawMessage{"a": {"vision": json.RawMessage(`{"input_modalities":["text","image"],"priority":0,"supports_image_detail_original":true,"model_messages":{"instructions_template":"","tools":{}}}`)}}}
	d, err := catalog.Resolve("a", "vision")
	if err != nil || !d.Vision() || d.Priority != 0 || *d.ModelMessages.InstructionsTemplate != "" || *d.ModelMessages.Tools["browser"].Description != "base" {
		t.Fatalf("descriptor=%+v err=%v", d, err)
	}
	catalog.Models["a"]["vision"] = json.RawMessage(`{"typo":true}`)
	if _, err := catalog.Resolve("a", "vision"); err == nil {
		t.Fatal("bad override fell back")
	}
	settings := DefaultAgentSettings()
	settings.Plugins = map[string]PluginConfig{}
	p := Profile{ID: "test", Revision: 1, Name: "test", Provider: "mock", Model: "test", AuthRef: "provider:mock", Params: Parameters{MaxOutputTokens: 100, Agent: settings}, ToolPolicy: ToolPolicy{SummaryReadOnly: true, Unattended: "runtime"}}
	spec, err := p.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if err := spec.Validate(); err != nil {
		t.Fatal(err)
	}
	spec.Model = "tampered"
	if err := spec.Validate(); err == nil {
		t.Fatal("tamper undetected")
	}
	settings.ContextReserve = settings.ContextWindow
	if err := settings.Validate(); err == nil {
		t.Fatal("invalid context accepted")
	}
}
func TestEventGateAndChanges(t *testing.T) {
	gate := NewEventGate(2, "turn")
	if gate.Accept(AgentEvent{Generation: 1, TurnID: "turn", Seq: 1}) {
		t.Fatal("old generation")
	}
	event := AgentEvent{Generation: 2, TurnID: "turn", Seq: 1, Update: Update{Type: "text_delta"}}
	if !gate.Accept(event) || gate.Accept(event) {
		t.Fatal("sequence gate")
	}
	event.Seq = 2
	event.Update = Update{Type: "turn_finished", Outcome: &Success}
	if !gate.Accept(event) || gate.Finish() != Success {
		t.Fatal("terminal")
	}
	event.Seq = 3
	if gate.Accept(event) {
		t.Fatal("post-terminal accepted")
	}
	changes := &Changes{}
	subscription := changes.Subscribe()
	defer subscription.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if value, err := subscription.Next(ctx); err != nil || len(value) != 0 {
		t.Fatal(value, err)
	}
	for range 10000 {
		changes.Notify("tasks")
	}
	changes.Notify("machines")
	value, err := subscription.Next(ctx)
	if err != nil || value["tasks"] != 10000 || value["machines"] != 10001 {
		t.Fatal(value, err)
	}
}

func TestDecodeJSONReceiptTimestamps(t *testing.T) {
	var receipt struct {
		CreatedAt time.Time `json:"created_at"`
	}
	if err := DecodeJSON([]byte(`{"created_at":"2026-10-23T00:00:00Z"}`), &receipt); err != nil || receipt.CreatedAt.IsZero() {
		t.Fatal(receipt, err)
	}
	for _, raw := range []string{`{"created_at":null}`, `{"created_at":{}}`, `{"created_at":"invalid"}`} {
		if err := DecodeJSON([]byte(raw), &receipt); err == nil {
			t.Fatal("accepted invalid timestamp", raw)
		}
	}
}
