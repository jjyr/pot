package executor

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/plugin"
)

func TestGitHubCLIReceivesCredentialAndArgumentVector(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node unavailable")
	}
	bin := t.TempDir()
	// The fixture checks argv literally and reports only whether authentication
	// was supplied. No external account or network is involved.
	program := "#!/bin/sh\n[ -n \"$GH_TOKEN\" ] || exit 4\n[ \"$1\" = 'api' ] || exit 5\n[ \"$2\" = 'literal; no shell expansion' ] || exit 6\nprintf authenticated\n"
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(program), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	bundle, err := plugin.LoadPlugin("../../plugins/github")
	if err != nil {
		t.Fatal(err)
	}
	e, r := fixture(t)
	r.Operation = "plugin_call"
	r.Environment = map[string]string{"GH_TOKEN": "fixture-private-token"}
	r.Args = map[string]any{"plugin": bundle, "tool": "github", "mode": "text", "input": map[string]any{"args": []any{"api", "literal; no shell expansion"}}}
	for range 10 {
		result, err := e.Execute(context.Background(), r, nil)
		raw, _ := json.Marshal(result)
		if err != nil || result["isError"] == true || !strings.Contains(string(raw), "authenticated") {
			t.Fatal(string(raw), err)
		}
		if strings.Contains(string(raw), "fixture-private-token") {
			t.Fatal("credential leaked in plugin result")
		}
	}
	if ids, err := e.plugins.inventory(); err != nil || len(ids) != 0 {
		t.Fatal("call-scoped plugins retained processes", ids, err)
	}
}

func counterBundle(t *testing.T) core.PluginBundle {
	t.Helper()
	b := core.PluginBundle{Manifest: core.PluginManifest{SchemaVersion: 2, ID: "counter", Version: "1", Contributes: core.PluginContributions{Tools: &core.PluginTools{Runtime: "node", Execution: "runtime", Entrypoint: "index.mjs", Files: []string{"index.mjs"}, Requirements: []string{"node"}, Tools: []json.RawMessage{json.RawMessage(`{"name":"count","description":"count","inputSchema":{"type":"object","properties":{"session_id":{"type":"string"},"reset":{"type":"boolean"}},"additionalProperties":false}}`)}}}}, Files: map[string]string{"index.mjs": "export function createPlugin({emit}) { let n=0; return {async execute(){emit({type:'text',text:String(++n)});},async dispose(){}}; }"}}
	var err error
	b.Digest, err = b.Hash()
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func pluginArgs(t *testing.T, b core.PluginBundle, input map[string]any) map[string]any {
	t.Helper()
	encoded, _ := json.Marshal(b)
	var wire map[string]any
	json.Unmarshal(encoded, &wire)
	result := map[string]any{"plugin": wire, "tool": "count", "mode": "text", "input": input}
	if id, ok := input["session_id"]; ok {
		result["session_id"] = id
	}
	return result
}
func TestPluginPersistentStateBindingResetAndDrain(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node unavailable")
	}
	e, r := fixture(t)
	bundle := counterBundle(t)
	first := call(t, e, r, "plugin_call", pluginArgs(t, bundle, map[string]any{}))
	id := first["session_id"].(string)
	next := call(t, e, r, "plugin_call", pluginArgs(t, bundle, map[string]any{"session_id": id}))
	content := next["content"].([]any)
	if content[0].(map[string]any)["text"] != "2" {
		t.Fatal(next)
	}
	changed := bundle
	changed.Manifest.Version = "changed"
	changed.Digest, _ = changed.Hash()
	request := r
	request.Operation = "plugin_call"
	request.Args = pluginArgs(t, changed, map[string]any{"session_id": id})
	if _, err := e.Execute(context.Background(), request, nil); err == nil {
		t.Fatal("accepted changed pinned package")
	}
	result := call(t, e, r, "plugin_call", pluginArgs(t, bundle, map[string]any{"session_id": id, "reset": true}))
	if result["closed"] != true {
		t.Fatal(result)
	}
	request.Args = pluginArgs(t, bundle, map[string]any{"session_id": id})
	if _, err := e.Execute(context.Background(), request, nil); err == nil {
		t.Fatal("resumed closed plugin")
	}
	first = call(t, e, r, "plugin_call", pluginArgs(t, bundle, map[string]any{}))
	if first["session_id"] == id {
		t.Fatal("reused disposed identity")
	}
	if err := e.Drain(); err != nil {
		t.Fatal(err)
	}
	result = call(t, e, r, "exec_command", map[string]any{"cmd": "printf next-turn"})
	if result["stdout"] != "next-turn" {
		t.Fatal(result)
	}
}
func TestPluginTargetIdleAndUnknownSession(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node unavailable")
	}
	e, r := fixture(t)
	r.Policy.SessionIdleTimeoutMS = 30
	bundle := counterBundle(t)
	first := call(t, e, r, "plugin_call", pluginArgs(t, bundle, map[string]any{}))
	id := first["session_id"].(string)
	time.Sleep(60 * time.Millisecond)
	request := r
	request.Operation = "plugin_call"
	request.Args = pluginArgs(t, bundle, map[string]any{"session_id": id})
	if _, err := e.Execute(context.Background(), request, nil); err == nil {
		t.Fatal("resumed idle plugin")
	}
	bundle.Manifest.Contributes.Tools.Execution = "sandbox"
	bundle.Digest, _ = bundle.Hash()
	request.Args = pluginArgs(t, bundle, map[string]any{})
	if _, err := e.Execute(context.Background(), request, nil); err == nil {
		t.Fatal("started Sandbox-only plugin on Host")
	}
}
