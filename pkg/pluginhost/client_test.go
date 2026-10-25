//go:build darwin || linux

package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func newFixture(t *testing.T, source, mode string) *Client {
	t.Helper()
	return newFixtureWithWorker(t, source, mode, nil)
}

func newFixtureWithWorker(t *testing.T, source, mode string, worker []string) *Client {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node unavailable: retained plugin protocol probe requires Node")
	}
	c, err := New(Config{Node: node, ProcessWorker: worker, Identity: "trusted-fixture-digest", Entrypoint: "index.mjs", Files: map[string]string{"index.mjs": source}, Mode: mode})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Errorf("cleanup: %v", err)
		}
	})
	return c
}

func execute(t *testing.T, c *Client, input map[string]any) Result {
	t.Helper()
	result, err := c.Execute(context.Background(), "fixture", input)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestSupervisedHostWorkingDirectory(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node unavailable: plugin startup probe requires Node")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, configured := range []bool{false, true} {
		name := "inherited"
		if configured {
			name = "workspace"
		}
		t.Run(name, func(t *testing.T) {
			workspace := ""
			expected, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			if configured {
				workspace = t.TempDir()
				expected, err = filepath.EvalSymlinks(workspace)
				if err != nil {
					t.Fatal(err)
				}
			}
			c, err := New(Config{
				Node: node, ProcessWorker: []string{binary}, Workspace: workspace,
				Identity: "working-directory-fixture", Entrypoint: "index.mjs", Mode: "text",
				Files: map[string]string{"index.mjs": `export function createPlugin({emit}) {
 return {async execute(){emit({type:'text',text:process.cwd()});},async dispose(){}};
}`},
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := c.Close(); err != nil {
					t.Errorf("cleanup: %v", err)
				}
			})
			result := execute(t, c, map[string]any{})
			if result.IsError || len(result.Content) != 1 || result.Content[0].Text != expected {
				t.Fatalf("plugin did not run in %q: %+v", expected, result)
			}
		})
	}
}

func TestPersistentStateErrorAndDisposal(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "disposed")
	markerJSON, _ := json.Marshal(marker)
	c := newFixture(t, `import {writeFile} from 'node:fs/promises';
export function createPlugin({emit,mode}) {
 let n=0;
 return {async execute({input}) {
  emit({type:'text',text:JSON.stringify({n:++n,mode})});
  if(input.fail) throw new Error('fixture failure');
 },async dispose(){await writeFile(`+string(markerJSON)+`, 'done');}};
}`, "text")
	first := execute(t, c, map[string]any{})
	if first.SessionID == "" || len(first.Content) != 1 || first.Content[0].Text != `{"n":1,"mode":"text"}` {
		t.Fatalf("unexpected result: %+v", first)
	}
	failed := execute(t, c, map[string]any{"fail": true, "session_id": first.SessionID})
	if !failed.IsError || !strings.Contains(failed.Error, "fixture failure; prior effects may have occurred") || failed.SessionID != first.SessionID {
		t.Fatalf("JS error lost state/content: %+v", failed)
	}
	third := execute(t, c, map[string]any{})
	if !strings.Contains(third.Content[0].Text, `"n":3`) {
		t.Fatalf("state not persistent: %+v", third)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "done" {
		t.Fatalf("dispose was not awaited: %s %v", data, err)
	}
	if _, err := c.Execute(context.Background(), "fixture", map[string]any{}); !errors.Is(err, ErrSessionEnded) {
		t.Fatalf("closed session reusable: %v", err)
	}
}

func TestPersistentInvocationRechecksAdmissionBeforeSendingCode(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "executed")
	path, _ := json.Marshal(marker)
	c := newFixture(t, `import {writeFileSync} from 'node:fs'; export function createPlugin(){return {async execute(){writeFileSync(`+string(path)+`,'executed')},async dispose(){}}}`, "text")
	rejected := errors.New("permission revision changed")
	c.admit = func() (func(), error) { return nil, rejected }
	if _, err := c.Execute(context.Background(), "fixture", map[string]any{}); !errors.Is(err, rejected) {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("revoked session received code", err)
	}
}

func TestTypedImagesModesAndNewestImage(t *testing.T) {
	source := `export function createPlugin({emit}) {return {async execute(){
 emit({type:'image',mimeType:'image/png',data:'YQ==',width:1,height:1});
 emit({type:'text',text:'between'});
 emit({type:'image',mimeType:'image/jpeg',data:'Yg==',width:2,height:3});
 },async dispose(){}};}`
	mixed := execute(t, newFixture(t, source, "mixed"), map[string]any{})
	if mixed.IsError || len(mixed.Content) != 2 || mixed.Content[0].Text != "between" || mixed.Content[1].Data != "Yg==" || mixed.Content[1].Width != 2 {
		t.Fatalf("typed/newest image lost: %+v", mixed)
	}
	text := execute(t, newFixture(t, source, "text"), map[string]any{})
	if !text.IsError || len(text.Content) != 0 {
		t.Fatalf("text mode accepted image: %+v", text)
	}
}

func TestRestrictedEnvironmentAndPrivateDirectory(t *testing.T) {
	t.Setenv("POT_PLUGIN_SECRET_FIXTURE", "must-not-leak")
	t.Setenv("NODE_OPTIONS", "--invalid-option-that-must-not-be-inherited")
	c := newFixture(t, `import {stat} from 'node:fs/promises';
export function createPlugin({emit}) {return {async execute(){
 emit({type:'text',text:JSON.stringify({secret:process.env.POT_PLUGIN_SECRET_FIXTURE??null,options:process.env.NODE_OPTIONS??null,tmp:process.env.TMPDIR,mode:(await stat(process.env.TMPDIR)).mode&511})});
},async dispose(){}};}`, "text")
	result := execute(t, c, map[string]any{})
	var info struct {
		Secret, Options *string
		Tmp             string
		Mode            int
	}
	if err := json.Unmarshal([]byte(result.Content[0].Text), &info); err != nil {
		t.Fatal(err)
	}
	if info.Secret != nil || info.Options != nil || info.Mode != 0700 || info.Tmp != c.directory {
		t.Fatalf("unsafe host context: %+v", info)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(info.Tmp); !os.IsNotExist(err) {
		t.Fatalf("private directory survived: %v", err)
	}
}

func TestConfiguredNodePrecedesOtherToolsForPluginChildren(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node unavailable")
	}
	other := t.TempDir()
	if err := os.WriteFile(filepath.Join(other, "node"), []byte("#!/bin/sh\nexit 99\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", other+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, override := range []bool{false, true} {
		t.Run(strconv.FormatBool(override), func(t *testing.T) {
			config := Config{Node: node, Identity: "trusted-fixture-digest", Entrypoint: "index.mjs", Mode: "text", Files: map[string]string{"index.mjs": `import {execFileSync} from 'node:child_process';
export function createPlugin({emit}) {return {async execute(){
 emit({type:'text',text:execFileSync('node',['-p','process.execPath'],{encoding:'utf8'}).trim()});
},async dispose(){}};}`}}
			if override {
				config.Environment = map[string]string{"PATH": other}
			}
			c, err := New(config)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = c.Close() })
			result := execute(t, c, map[string]any{})
			want, err := filepath.EvalSymlinks(node)
			if err != nil {
				t.Fatal(err)
			}
			if result.IsError || len(result.Content) != 1 || result.Content[0].Text != want {
				t.Fatalf("plugin child did not use configured Node: %+v, want %s", result, want)
			}
		})
	}
}

func TestCancellationInvalidatesAndCleansDescendants(t *testing.T) {
	c := newFixtureWithWorker(t, `import {spawn} from 'node:child_process';
export function createPlugin({emit}) {return {async execute({input}){
 if(input.block) await new Promise(()=>{});
 else {const child=spawn('sleep',['60'],{stdio:'ignore'});emit({type:'text',text:String(child.pid)});}
},async dispose(){}};}`, "text", testProcessWorker(t))
	first := execute(t, c, map[string]any{})
	pid, err := strconv.Atoi(first.Content[0].Text)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err = c.Execute(ctx, "fixture", map[string]any{"block": true})
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "prior effects may have occurred") {
		t.Fatalf("cancellation lost uncertain effect: %v", err)
	}
	if _, err = c.Execute(context.Background(), "fixture", map[string]any{}); !errors.Is(err, ErrSessionEnded) {
		t.Fatalf("cancelled session reusable: %v", err)
	}
	if err = c.Close(); err != nil {
		t.Fatalf("cancellation cleanup unconfirmed: %v", err)
	}
	if err = syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("descendant survived cancellation: %v", err)
	}
	if _, err = os.Stat(c.directory); !os.IsNotExist(err) {
		t.Fatalf("temporary directory survived cancellation: %v", err)
	}
}

func TestNoRetryAfterProcessLoss(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "effects")
	markerJSON, _ := json.Marshal(marker)
	c := newFixture(t, `import {appendFileSync} from 'node:fs';
export function createPlugin(){return {async execute(){appendFileSync(`+string(markerJSON)+`,'effect\n');process.exit(3);},async dispose(){}};}`, "text")
	if _, err := c.Execute(context.Background(), "fixture", map[string]any{}); err == nil {
		t.Fatal("process loss succeeded")
	}
	if _, err := c.Execute(context.Background(), "fixture", map[string]any{}); !errors.Is(err, ErrSessionEnded) {
		t.Fatal(err)
	}
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != "effect\n" {
		t.Fatalf("effect retried: %q %v", data, err)
	}
}

func TestMalformedWireEndsSession(t *testing.T) {
	for _, response := range []string{
		`{"content":[{"type":"text"}]}`,
		`{"content":[{"type":"text","text":null}]}`,
		`{"content":[],"isError":false,"code":"browser_unavailable","execution_status":"not_started"}`,
		`{"content":[],"isError":true,"error":"invalid","execution_status":"cleanup_unconfirmed"}`,
		`{"content":[{"type":"image","mimeType":"image/png","data":"invalid","width":1,"height":1}]}`,
	} {
		t.Run(response, func(t *testing.T) {
			encoded, _ := json.Marshal(response + "\n")
			c := newFixture(t, `export function createPlugin(){return {async execute(){process.stdout.write(`+string(encoded)+`);await new Promise(()=>{});},async dispose(){}};}`, "mixed")
			if _, err := c.Execute(context.Background(), "fixture", map[string]any{}); err == nil {
				t.Fatal("invalid wire content accepted")
			}
			if _, err := c.Execute(context.Background(), "fixture", map[string]any{}); !errors.Is(err, ErrSessionEnded) {
				t.Fatal(err)
			}
		})
	}
}

func TestPackageConfinement(t *testing.T) {
	for _, name := range []string{"../escape.mjs", "/escape.mjs", "nested/../escape.mjs", "nested\\escape.mjs", "./escape.mjs"} {
		if _, err := New(Config{Identity: "trusted", Mode: "text", Entrypoint: name, Files: map[string]string{name: ""}}); err == nil {
			t.Fatalf("unsafe package accepted: %q", name)
		}
	}
}

func TestPluginDiagnosticsCannotComeFromEvaluatedException(t *testing.T) {
	c := newFixture(t, `export function createPlugin(){return {async execute(){throw Object.assign(new Error('failed after execution'),{code:'browser_unavailable',execution_status:'not_started'})},async dispose(){}}}`, "text")
	result := execute(t, c, map[string]any{})
	if !result.IsError || result.Code != "" || result.ExecutionStatus != "" || !strings.Contains(result.Error, "prior effects may have occurred") {
		t.Fatal("exception claimed a pre-execution rejection", result)
	}
}

func TestRetainedComputerUseREPLWithoutStartingBrowser(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node unavailable: retained computer-use REPL probe requires Node")
	}
	// Availability checks an executable; this probe never launches a browser.
	t.Setenv("POT_BROWSER_EXECUTABLE", node)
	files := map[string]string{}
	for _, name := range []string{"index.mjs", "browser.mjs", "instructions.md"} {
		source, err := os.ReadFile(filepath.Join("../../plugins/computer-use", name))
		if err != nil {
			t.Fatal(err)
		}
		files[name] = string(source)
	}
	c, err := New(Config{Node: node, Identity: "trusted-builtin-fixture", Entrypoint: "index.mjs", Files: files, Mode: "text"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Error(err)
		}
	})
	first, err := c.Execute(context.Background(), "cua_repl", map[string]any{"code": "let counter = await Promise.resolve(41); nodeRepl.write(counter);"})
	if err != nil {
		t.Fatal(err)
	}
	if first.IsError || len(first.Content) != 2 || !strings.Contains(first.Content[0].Text, "headless Chromium in this execution machine") || first.Content[1].Text != "41" {
		t.Fatalf("retained Node REPL failed: %+v", first)
	}
	next, err := c.Execute(context.Background(), "cua_repl", map[string]any{"session_id": first.SessionID, "code": "counter++; nodeRepl.write(counter);"})
	if err != nil {
		t.Fatal(err)
	}
	if next.IsError || len(next.Content) != 1 || next.Content[0].Text != "42" {
		t.Fatalf("retained REPL state lost: %+v", next)
	}
}
