package control

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"database/sql"
	"encoding/json"
	"github.com/jjyr/pot/internal/gateway"
	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/execution"
	"github.com/jjyr/pot/pkg/protocol"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Opt-in, isolated production Agent/Task/Executor against the machine's configured model.
func TestConfiguredModelTaskWorkspacesLive(t *testing.T) {
	if os.Getenv("POT_LIVE") != "1" || os.Getenv("POT_WORKSPACE_LIVE_ROOT") == "" {
		t.Skip("requires explicit configured-machine workspace live opt-in")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	production := os.Getenv("POT_WORKSPACE_LIVE_ROOT")
	raw, err := os.ReadFile(filepath.Join(production, "profiles.json"))
	if err != nil {
		t.Fatal(err)
	}
	var config profileFile
	if err = json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	if len(config.Profiles) == 0 {
		t.Fatal("no configured model")
	}
	profile := config.Profiles[0]
	db, err := sql.Open("sqlite", "file:"+filepath.Join(production, "control.sqlite")+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var metadata string
	if err = db.QueryRowContext(ctx, "SELECT metadata FROM providers WHERE id=?", profile.Provider).Scan(&metadata); err != nil {
		t.Fatal(err)
	}
	var provider gateway.Provider
	if err = json.Unmarshal([]byte(metadata), &provider); err != nil {
		t.Fatal(err)
	}
	key, err := (secrets.SystemKey{Account: "reference-live"}).Key(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	var nonce, encrypted, tag []byte
	if err = db.QueryRowContext(ctx, "SELECT nonce,ciphertext,tag FROM secrets WHERE id=?", provider.SecretRef).Scan(&nonce, &encrypted, &tag); err != nil {
		t.Fatal(err)
	}
	credential, err := aead.Open(nil, nonce, append(encrypted, tag...), []byte(provider.SecretRef))
	if err != nil {
		t.Fatal("provider credential authentication failed")
	}
	defer clear(credential)

	var requests atomic.Int32
	var mu sync.Mutex
	var bodies []string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) > 18 {
			http.Error(w, "live request bound reached", http.StatusTooManyRequests)
			return
		}
		raw, e := io.ReadAll(io.LimitReader(r.Body, 4*1024*1024))
		if e != nil {
			http.Error(w, "body read failed", 400)
			return
		}
		mu.Lock()
		bodies = append(bodies, string(raw))
		mu.Unlock()
		req, e := http.NewRequestWithContext(r.Context(), "POST", provider.Endpoint, strings.NewReader(string(raw)))
		if e != nil {
			http.Error(w, "request failed", 500)
			return
		}
		req.Header.Set("Authorization", "Bearer "+string(credential))
		req.Header.Set("Content-Type", "application/json")
		response, e := http.DefaultClient.Do(req)
		if e != nil {
			http.Error(w, "upstream connection failed", 502)
			return
		}
		defer response.Body.Close()
		w.Header().Set("Content-Type", response.Header.Get("Content-Type"))
		w.WriteHeader(response.StatusCode)
		io.Copy(w, response.Body)
	}))
	defer proxy.Close()
	t.Logf("configured model=%s protocol=%s", profile.Model, provider.Protocol)
	root := t.TempDir()
	manager, _ := execution.New("workspace-live", "boot", execution.DefaultLimits())
	keys, _ := secrets.NewMemoryKey(make([]byte, 32))
	resources := os.Getenv("POT_WORKSPACE_LIVE_RESOURCES")
	options := Options{Context: ctx, Root: root, RuntimeID: "workspace-live", BootID: "boot", Manager: manager, Keys: keys}
	if resources != "" {
		options.Node = filepath.Join(resources, "node/bin/node")
		options.SandboxDirectory = filepath.Join(resources, "sandbox")
	}
	service, err := Open(options)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		service.Close()
		manager.Stop()
		drain, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		if e := manager.Drain(drain); e != nil {
			t.Error(e)
		}
	}()
	service.Registry.Create(ctx, "create", "Workspace qualification")
	saved, err := service.Profiles.Save(ctx, SaveProfile{RequestID: "live", ProfileID: "live", Name: "Workspace qualification", Model: profile.Model, Endpoint: proxy.URL, Protocol: provider.Protocol, APIKey: "isolated-proxy", MaxOutputTokens: 2048, ReasoningEffort: profile.Params.ReasoningEffort})
	if err != nil {
		t.Fatal(err)
	}
	spec, err := service.Profiles.Resolve(saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	spec.Params.Agent.BuiltinTools = []string{"exec_command", "apply_patch", "write_stdin"}
	spec.Params.Agent.Plugins = map[string]core.PluginConfig{}
	spec.Params.Agent.AutoCompact = false
	spec.Params.Agent.ContextWindow = 500000
	spec.ContextWindow = 500000
	spec.Params.Agent.RequestTimeoutMS = 180000
	if err = spec.Rehash(); err != nil {
		t.Fatal(err)
	}
	conversation, err := service.Conversation(ctx, "main")
	if err != nil {
		t.Fatal(err)
	}
	if err = conversation.Configure(ctx, spec, 0); err != nil {
		t.Fatal(err)
	}
	// Isolated local bare remote proves refresh without touching any user repository.
	remote := filepath.Join(t.TempDir(), "remote.git")
	fixtureGit(t, t.TempDir(), "init", "--bare", "--initial-branch=trunk", remote)
	source := filepath.Join(t.TempDir(), "sample-project")
	fixtureGit(t, t.TempDir(), "clone", remote, source)
	writeTaskFixture(t, filepath.Join(source, "AGENTS.md"), "Every proof.txt must include PROJECT_RULE_OLD on its own line.\n")
	fixtureGit(t, source, "add", ".")
	fixtureGit(t, source, "commit", "-m", "old")
	fixtureGit(t, source, "push", "origin", "trunk")
	upstream := filepath.Join(t.TempDir(), "upstream")
	fixtureGit(t, t.TempDir(), "clone", remote, upstream)
	writeTaskFixture(t, filepath.Join(upstream, "AGENTS.md"), "Every proof.txt must include PROJECT_RULE_FRESH on its own line.\n")
	fixtureGit(t, upstream, "add", ".")
	fixtureGit(t, upstream, "commit", "-m", "fresh")
	fixtureGit(t, upstream, "push", "origin", "trunk")
	fresh := fixtureGit(t, upstream, "rev-parse", "HEAD")
	old := fixtureGit(t, source, "rev-parse", "HEAD")
	direct := t.TempDir()
	writeTaskFixture(t, filepath.Join(direct, "AGENTS.md"), "Every proof.txt must include DIRECT_RULE_TOKEN on its own line.\n")
	cases := []struct {
		name      string
		workspace protocol.TaskWorkspace
		rule      string
	}{{"worktree", protocol.TaskWorkspace{Kind: "worktree", ProjectDir: source}, "PROJECT_RULE_FRESH"}, {"directory", protocol.TaskWorkspace{Kind: "directory", ProjectDir: direct}, "DIRECT_RULE_TOKEN"}, {"temporary", protocol.TaskWorkspace{}, ""}}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			policy := "allow"
			if os.Getenv("POT_WORKSPACE_LIVE_PROTECTED") == "1" && test.name != "directory" {
				policy = "ask"
			}
			if err := service.Permissions.Configure("workspace-live", MachinePolicy{ExecutionAccess: policy}); err != nil {
				t.Fatal(err)
			}
			sub := service.Tasks.Subscribe()
			defer sub.Close()
			task, err := service.Tasks.AcceptLocal(ctx, "main", protocol.CreateTask{RequestID: "workspace-" + test.name, Workspace: test.workspace, Text: "Verify prepared workspace\nUse apply_patch to create proof.txt containing WORKSPACE_LIVE_OK and any line required by the project instructions. Then use exec_command to verify pwd and proof.txt. For a Git repository also check git status --short. For verification use portable shell commands pwd, cat, printf, test and git; rg is not installed. Work in the supplied cwd, preserve existing project files and finish briefly. Do not start services or make external changes."})
			if err != nil {
				t.Fatal(err)
			}
			for {
				current, e := service.Tasks.Get(ctx, "main", task.Info.ID)
				if e != nil {
					t.Fatal(e)
				}
				if current.Info.Phase == protocol.TaskWaitingApproval {
					events, _ := service.TaskEvents(ctx, "main", task.Info.ID, 0, 100)
					for _, event := range events.Events {
						u := event.Event.Update
						if u.Type == "tool_finished" {
							t.Logf("prior tool %s: %s", u.Name, u.Result)
						}
					}
					t.Fatal("unexpected host approval in workspace qualification")
				}
				if current.Info.Phase.Scope() != "active" {
					if current.Info.Phase != protocol.TaskComplete {
						t.Fatalf("Task failed: %s", current.Error)
					}
					break
				}
				if _, e = sub.Next(ctx); e != nil {
					t.Fatal(e)
				}
			}
			w, err := service.RunJournal.TaskWorkspace(ctx, task.Info.ID)
			if err != nil || w == nil {
				t.Fatal(w, err)
			}
			if test.name == "worktree" {
				defer func() {
					fixtureGit(t, source, "worktree", "remove", "--force", w.Path)
					os.Remove(filepath.Dir(w.Path))
				}()
				if w.Commit != fresh || fixtureGit(t, source, "rev-parse", "HEAD") != old {
					t.Fatal("base/source binding changed")
				}
				home, _ := os.UserHomeDir()
				if !strings.HasPrefix(w.Path, filepath.Join(home, ".pot", "worktrees")) {
					t.Fatal("worktree location", w.Path)
				}
			}
			if test.name == "temporary" {
				defer os.RemoveAll(w.Path)
			}
			data, err := service.ReadTaskFile(ctx, "main", task.Info.ID, "proof.txt", 4096)
			if err != nil || !strings.Contains(string(data), "WORKSPACE_LIVE_OK") || test.rule != "" && !strings.Contains(string(data), test.rule) {
				t.Fatalf("artifact/instructions mismatch: %s %v", data, err)
			}
			events, err := service.TaskEvents(ctx, "main", task.Info.ID, 0, 100)
			if err != nil {
				t.Fatal(err)
			}
			patch, command := false, false
			for _, event := range events.Events {
				u := event.Event.Update
				if u.Type == "tool_finished" {
					if u.Failed {
						t.Fatalf("live tool failed: %s %s", u.Name, u.Result)
					}
					patch = patch || u.Name == "apply_patch"
					command = command || u.Name == "exec_command"
				}
			}
			if !patch || !command {
				t.Fatal("model did not exercise patch and commands")
			}
			mu.Lock()
			found := false
			for _, body := range bodies {
				if strings.Contains(body, w.Path) && strings.Contains(body, "cwd:") {
					found = true
					if test.name == "worktree" && strings.Contains(body, source) {
						t.Error("source directory leaked into Worker workspace metadata")
					}
				}
			}
			mu.Unlock()
			if !found {
				t.Fatal("Worker missing prepared cwd")
			}
			t.Logf("workspace=%s path=%s policy=%s patch/command/download passed", test.name, w.Path, policy)
		})
	}
	t.Logf("real model requests=%d", requests.Load())
}
