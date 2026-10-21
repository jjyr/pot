//go:build darwin || linux

package pluginhost

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jjyr/pot/pkg/plugin"
)

func TestGitHubReadToolInstalledSchemaThroughNodeHost(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node unavailable")
	}
	root, _ := filepath.Abs("../../plugins/github")
	p, err := plugin.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	// Validate the installed manifest, then execute its actual files through the
	// production supervised JSON-lines host. Only the fake fetch wrapper is added.
	files := map[string]string{}
	for _, name := range p.Manifest.Contributes.Tools.Files {
		raw, e := os.ReadFile(filepath.Join(root, name))
		if e != nil {
			t.Fatal(e)
		}
		files[name] = string(raw)
	}
	files["fixture.mjs"] = `import {createPlugin} from './index.mjs';
 globalThis.fetch=async (url,init)=>{if(init.method!=='GET'||!String(url).startsWith('https://api.github.com/notifications?'))throw Error('unexpected request');return Response.json([{id:'123',reason:'mention',updated_at:'2026-01-01T00:00:00Z',repository:{full_name:'me/repo'},subject:{type:'Issue',title:'Fixture',url:'https://api.github.com/repos/me/repo/issues/1'}}]);};
 export {createPlugin};`
	c, err := New(Config{Node: node, Identity: "fixture", Entrypoint: "fixture.mjs", Files: files, Environment: map[string]string{"GH_TOKEN": "fake-token"}, Mode: "text"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	result, err := c.Execute(context.Background(), "github_read_notifications", map[string]any{"reasons": []string{"mention"}, "per_page": 30})
	if err != nil || result.IsError {
		t.Fatal(result, err)
	}
	if len(result.Content) != 1 {
		t.Fatal(result)
	}
	var body struct {
		Items []struct {
			Thread string `json:"thread_id"`
		} `json:"items"`
	}
	if err = json.Unmarshal([]byte(result.Content[0].Text), &body); err != nil || len(body.Items) != 1 || body.Items[0].Thread != "123" {
		t.Fatal(result, err)
	}
}
