package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/plugin"
)

func mcpFixture(t *testing.T, body string) (plugin.Package, []string) {
	t.Helper()
	root := t.TempDir()
	source := `import {createInterface} from 'node:readline';const send=v=>process.stdout.write(JSON.stringify(v)+'\n');createInterface({input:process.stdin}).on('line',async line=>{const f=JSON.parse(line);if(!f.method||f.id===undefined)return;` + body + `});`
	if err := os.WriteFile(filepath.Join(root, "server.mjs"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	p := plugin.Package{Root: root, Manifest: core.PluginManifest{SchemaVersion: 2, ID: "fixture", Version: "1", Contributes: core.PluginContributions{MCP: &core.PluginMCP{Runtime: "node", Execution: "control", Entrypoint: "server.mjs", Files: []string{"server.mjs"}, Credentials: core.CredentialBindings{"SERVICE_TOKEN": {Service: "github"}}}}}}
	raw, _ := json.Marshal(p.Manifest)
	if err := os.WriteFile(filepath.Join(root, "plugin.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	worker, _ := os.Executable()
	return p, []string{worker}
}

const initializeMCP = `if(f.method==='initialize'){send({jsonrpc:'2.0',id:f.id,result:{protocolVersion:'2025-06-18',capabilities:{experimental:{'pot/background-store':{version:1}}},serverInfo:{name:'fixture',version:'1'}}});return;}`

func TestMCPRealDiscoveryPaginationAndDeclaredCredentials(t *testing.T) {
	t.Setenv("GH_TOKEN", "ambient-must-not-leak")
	p, worker := mcpFixture(t, initializeMCP+`if(f.method==='tools/list'){const next=!!f.params.cursor;send({jsonrpc:'2.0',id:f.id,result:{tools:[{name:next?'second':'first',description:'fixture',inputSchema:{type:'object'}}],...(next?{}:{nextCursor:'page2'})}});return;}if(f.method==='tools/call')send({jsonrpc:'2.0',id:f.id,result:{content:[{type:'text',text:process.env.SERVICE_TOKEN==='fixture-private-token'&&!process.env.GH_TOKEN?'isolated':'bad environment'}],structuredContent:{ok:true}}});`)
	tools, r, err := RunMCP(context.Background(), "", p, map[string]string{"SERVICE_TOKEN": "fixture-private-token"}, worker, "second", json.RawMessage(`{}`), nil)
	if err != nil || len(tools) != 2 || len(r.Content) != 1 || r.Content[0].Text != "isolated" {
		t.Fatal(tools, r, err)
	}
	if _, _, err = RunMCP(context.Background(), "", p, map[string]string{"UNDECLARED": "value"}, worker, "", nil, nil); err == nil {
		t.Fatal("undeclared credential accepted")
	}
}

func TestMCPRejectsMalformedOutputAndCredentialDisclosure(t *testing.T) {
	for _, body := range []string{`send({jsonrpc:'1.0',id:f.id,result:{}});`, `send({jsonrpc:'2.0',id:f.id,result:{secret:process.env.SERVICE_TOKEN}});`} {
		p, worker := mcpFixture(t, body)
		if _, _, err := RunMCP(context.Background(), "", p, map[string]string{"SERVICE_TOKEN": "fixture-private-token"}, worker, "", nil, nil); err == nil || strings.Contains(err.Error(), "fixture-private-token") {
			t.Fatal("invalid output accepted or secret leaked", err)
		}
	}
}

func TestMCPCallbacksRequireNegotiatedExtensionAndCancellationClosesProcess(t *testing.T) {
	p, worker := mcpFixture(t, `send({jsonrpc:'2.0',id:'host',method:'pot/context',params:{}});`)
	called := false
	host := func(context.Context, string, json.RawMessage) (any, error) {
		called = true
		return nil, errors.New("should not run")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if _, _, err := RunMCP(ctx, "", p, nil, worker, "", nil, host); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if called {
		t.Fatal("callback executed before negotiation")
	}
}
