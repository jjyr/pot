package executor

import (
	"context"
	"encoding/json"
	"os/exec"
	"testing"
	"time"

	"github.com/jjyr/pot/pkg/core"
)

func counterBundle(t *testing.T) core.PluginBundle {
	t.Helper()
	b := core.PluginBundle{Manifest: core.PluginManifest{SchemaVersion: 1, ID: "counter", Version: "1", Runtime: "node", Execution: "host_or_sandbox", Entrypoint: "index.mjs", Files: []string{"index.mjs"}, Requirements: []string{"node"}, Tools: []json.RawMessage{json.RawMessage(`{"name":"count","description":"count","inputSchema":{"type":"object","properties":{"session_id":{"type":"string"},"reset":{"type":"boolean"}},"additionalProperties":false}}`)}}, Files: map[string]string{"index.mjs": "export function createPlugin({emit}) { let n=0; return {async execute(){emit({type:'text',text:String(++n)});},async dispose(){}}; }"}}
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
	bundle.Manifest.Execution = "sandbox"
	bundle.Digest, _ = bundle.Hash()
	request.Args = pluginArgs(t, bundle, map[string]any{})
	if _, err := e.Execute(context.Background(), request, nil); err == nil {
		t.Fatal("started Sandbox-only plugin on Host")
	}
}
