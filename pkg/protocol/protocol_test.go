package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestWireNullUnknownAndEventShape(t *testing.T) {
	event := Event{EventID: "e1", Type: "turn"}
	data, err := json.Marshal(event)
	if err != nil || string(data) != `{"event_id":"e1","cursor":null,"type":"turn","turn":null}` {
		t.Fatalf("%s %v", data, err)
	}
	var restored Event
	if err := DecodeStrict(data, &restored); err != nil {
		t.Fatal(err)
	}
	var request CreateTask
	for _, data := range []string{`{"request_id":"r","text":"x","environment_ref":{}}`, `{"request_id":"r","text":"x","resource":"shared"}`, `{"request_id":"r","text":"x","resource":""}`, `{"request_id":"r","text":null}`, `{"text":"x"}`, `{"request_id":"r","text":"x","request_id":"other"}`} {
		if err := DecodeStrict([]byte(data), &request); err == nil {
			t.Fatal(data)
		}
	}
	if err := DecodeStrict([]byte(`{"request_id":"r","text":"build","machine_id":"remote"}`), &request); err != nil {
		t.Fatal(err)
	}
	var spec ScheduleSpec
	if err := DecodeStrict([]byte(`{"title":"build","goal":"build","kind":"interval","every_minutes":60}`), &spec); err != nil {
		t.Fatal(err)
	}
	if spec.Timezone != "" || !spec.Enabled || spec.Notify != "changes" {
		t.Fatal(spec)
	}
}
func TestFileLinksReferencesAndRouting(t *testing.T) {
	task := "00000000-0000-4000-8000-000000000001"
	id, file, err := ParseFileLink("pot-file://" + task + "/reports/%E6%8A%A5%E5%91%8A%20a.pdf")
	if err != nil || id != task || file != "reports/报告 a.pdf" {
		t.Fatal(id, file, err)
	}
	for _, p := range []string{"../x", "%2Fetc/passwd", "a/%2e%2e/x", "a%5Cb", "x%00", "x?token=z", "x#z", ""} {
		if _, _, err := ParseFileLink("pot-file://" + task + "/" + p); err == nil {
			t.Fatal(p)
		}
	}
	machines := []MachineRoute{{"runner", []string{"linux", "task-runner"}}, {"control", DefaultLabels("macos")}}
	if selected, err := SelectMachine(machines, "control", References{}); err != nil || selected != "control" {
		t.Fatal("default placement followed labels instead of the owning Control", selected, err)
	}
	refs, err := ParseReferences("工作 /machine:runner /tag:linux /tag:linux /skill:pdf")
	if err != nil || len(refs.Tags) != 1 {
		t.Fatal(refs, err)
	}
	id, err = SelectMachine(machines, "control", refs)
	if err != nil || id != "runner" {
		t.Fatal(id, err)
	}
	refs.Tags = append(refs.Tags, "desktop")
	if _, err = SelectMachine(machines, "control", refs); err == nil {
		t.Fatal("capability fallback")
	}
	start, q, ok := CompletionQuery("中文 /desk", len("中文 /desk"))
	if !ok || start != 7 || q != "desk" {
		t.Fatal(start, q, ok)
	}
}
func TestSSEChunksAndBounds(t *testing.T) {
	decoder := SSEDecoder{}
	frames := []Frame{}
	for _, b := range []byte(": heartbeat\r\n\r\nevent: snapshot\r\ndata: 中文\r\ndata: next\r\n\r\n") {
		out, err := decoder.Push([]byte{b})
		if err != nil {
			t.Fatal(err)
		}
		frames = append(frames, out...)
	}
	if len(frames) != 1 || frames[0] != (Frame{"snapshot", "中文\nnext"}) {
		t.Fatal(frames)
	}
	if err := decoder.Finish(); err != nil {
		t.Fatal(err)
	}
	_, _ = decoder.Push([]byte("data: partial\n"))
	if decoder.Finish() == nil {
		t.Fatal("truncation accepted")
	}
	decoder = SSEDecoder{Limit: 32}
	if _, err := decoder.Push([]byte(strings.Repeat("x", 33))); err == nil {
		t.Fatal("bound not enforced")
	}
}

func TestTaskWorkspaceIntentValidation(t *testing.T) {
	for _, w := range []TaskWorkspace{{}, {ProjectDir: "/project"}, {Kind: "worktree", ProjectDir: "/project", Base: "main"}, {Kind: "directory", ProjectDir: "/project"}} {
		if err := w.Validate(); err != nil {
			t.Fatal(w, err)
		}
	}
	for _, w := range []TaskWorkspace{{Kind: "unknown"}, {Kind: "worktree"}, {Kind: "temporary", ProjectDir: "/project"}, {Kind: "directory", ProjectDir: "/project", Remote: "origin"}, {Kind: "worktree", ProjectDir: "/project", Base: "main", Commit: "deadbeef"}, {Kind: "directory", ProjectDir: "/project\nother"}} {
		if err := w.Validate(); err == nil {
			t.Fatalf("accepted invalid workspace: %+v", w)
		}
	}
}
