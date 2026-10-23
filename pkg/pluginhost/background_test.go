package pluginhost

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/plugin"
)

func TestBackgroundProtocolCannotForgeRouteOrLeakCredential(t *testing.T) {
	for _, scenario := range []struct {
		name, output string
		valid        bool
	}{
		{"valid", `{state:{cursor:1},events:[{id:'one',title:'Update',body:'external',occurred_at:'2026-01-01T00:00:00Z'}],delay_seconds:60,status:'ready'}`, true},
		{"removed_group", `{state:null,events:[{id:'one',title:'Update',body:'external',occurred_at:'2026-01-01T00:00:00Z',group:'unused'}],delay_seconds:60,status:'ready'}`, false},
		{"forged_sender", `{state:null,events:[],delay_seconds:60,status:'ready',from:'user'}`, false},
		{"forged_target", `{state:null,events:[{id:'one',title:'Update',body:'external',occurred_at:'2026-01-01T00:00:00Z',assistant:'other'}],delay_seconds:60,status:'ready'}`, false},
		{"credential_output", `{state:{token:process.env.SERVICE_TOKEN},events:[],delay_seconds:60,status:'ready'}`, false},
		{"unbounded_frequency", `{state:null,events:[],delay_seconds:0,status:'ready'}`, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			root := t.TempDir()
			manifest := `{"schema_version":3,"name":"example","version":"1","contributes":{"background":{"runtime":"node","execution":"control","entrypoint":"service.mjs","files":["service.mjs"],"credentials":{"SERVICE_TOKEN":{"service":"github"}}}}}`
			if err := os.WriteFile(filepath.Join(root, "plugin.json"), []byte(manifest), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "service.mjs"), []byte("export async function poll(input){if(input.version!==1)throw Error('version');return "+scenario.output+";}"), 0600); err != nil {
				t.Fatal(err)
			}
			p, err := plugin.Load(root)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = p.ToolBundle(); err == nil {
				t.Fatal("service became transferable tool")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			worker, _ := os.Executable()
			_, err = RunBackground(ctx, "", p, core.BackgroundRequest{Version: 1, Config: []byte(`{}`), State: []byte(`null`)}, map[string]string{"SERVICE_TOKEN": "fixture-private-token"}, []string{worker})
			if (err == nil) != scenario.valid {
				t.Fatalf("valid=%v err=%v", scenario.valid, err)
			}
		})
	}
}

func TestBackgroundCancellationUsesSupervisorForDetachedDescendants(t *testing.T) {
	root := t.TempDir()
	source := `import {spawn} from 'node:child_process';import {writeFileSync} from 'node:fs';export async function poll(){const child=spawn(process.execPath,['-e','setInterval(()=>{},1000)'],{detached:true,stdio:'ignore'});writeFileSync('child.pid',String(child.pid));await new Promise(()=>{});}`
	if err := os.WriteFile(filepath.Join(root, "service.mjs"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	p := plugin.Package{Root: root, Manifest: core.PluginManifest{SchemaVersion: 2, ID: "example", Version: "1", Contributes: core.PluginContributions{Background: &core.PluginBackground{Runtime: "node", Execution: "control", Entrypoint: "service.mjs", Files: []string{"service.mjs"}}}}}
	worker, _ := os.Executable()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, err := RunBackground(ctx, "", p, core.BackgroundRequest{Version: 1}, nil, []string{worker}); err == nil || err.Error() != "background exchange timed out" {
		t.Fatal("deadline diagnostic missing", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "child.pid"))
	if err != nil {
		t.Fatal("fixture child did not start", err)
	}
	pid, err := strconv.Atoi(string(data))
	if err != nil {
		t.Fatal(err)
	}
	if err = syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatal("supervised background descendant survived", pid, err)
	}
}

func TestBackgroundStreamAcknowledgesOnlyCommittedFrames(t *testing.T) {
	root := t.TempDir()
	source := `export async function stream(input,emit){await emit({state:{cursor:1},events:[],delay_seconds:60,status:'ready'});await emit({state:{cursor:2},events:[],delay_seconds:60,status:'ready'});await new Promise(()=>{});}`
	if err := os.WriteFile(filepath.Join(root, "service.mjs"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	p := plugin.Package{Root: root, Manifest: core.PluginManifest{SchemaVersion: 2, ID: "example", Version: "1", Contributes: core.PluginContributions{Background: &core.PluginBackground{Runtime: "node", Execution: "control", Mode: "stream", Entrypoint: "service.mjs", Files: []string{"service.mjs"}}}}}
	worker, _ := os.Executable()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	count := 0
	rejected := errors.New("transaction_rejected")
	err := RunBackgroundStream(ctx, "", p, core.BackgroundRequest{Version: 1, State: []byte(`null`), Config: []byte(`{}`)}, nil, []string{worker}, func(frame core.BackgroundResponse) error {
		count++
		if string(frame.State) != `{"cursor":`+strconv.Itoa(count)+`}` {
			t.Fatal("checkpoint order", string(frame.State))
		}
		if count == 2 {
			return rejected
		}
		return nil
	})
	if !errors.Is(err, rejected) || count != 2 {
		t.Fatal(count, err)
	}
}

func TestBackgroundStructuredCredentialsProtectTokenButNotPublicRoute(t *testing.T) {
	credential := `{"id":"public-device","token":"private-pairing-token"}`
	if containsBackgroundCredential([]byte(`{"pair":"public-device"}`), credential) {
		t.Fatal("public checkpoint rejected")
	}
	if !containsBackgroundCredential([]byte(`{"body":"private-pairing-token"}`), credential) {
		t.Fatal("nested token leaked")
	}
}
