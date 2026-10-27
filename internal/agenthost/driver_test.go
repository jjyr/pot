package agenthost

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jjyr/pot/pkg/agentlink"
)

func TestNativeEnvironmentReplacesInheritedIdentityWithoutChangingLogin(t *testing.T) {
	parent := []string{"HOME=/native/login", "CODEX_HOME=/native/config", "PATH=/usr/bin", "POT_AGENT_JOB_ID=stale-job", "POT_AGENT_RUN_ID=stale-run", "POT_AGENT_ASSISTANT_ID=wrong-actor", "POT_AGENT_MODE=workspace-write", "POT_AGENT_HOST_ROOT=/other", "POT_AGENT_RECEIVER_TOKEN=must-not-inherit"}
	for _, s := range []Session{
		{JobID: "task-a", RunID: "run-one", AssistantID: "assistant-a", Mode: "read-only", HostRoot: "/host-a"},
		{JobID: "task-a", RunID: "run-two", Mode: "workspace-write", HostRoot: "/host-a"},
		{JobID: "task-b", RunID: "run-other", AssistantID: "assistant-b", Mode: "read-only", HostRoot: "/host-b"},
	} {
		// A real child observes the exact context; no login/auth files are read.
		cmd := exec.Command("sh", "-c", `printf '%s\n' "$HOME" "$CODEX_HOME" "$POT_AGENT_ASSISTANT_ID" "$POT_AGENT_JOB_ID" "$POT_AGENT_RUN_ID" "$POT_AGENT_MODE" "$POT_AGENT_HOST_ROOT" "${POT_AGENT_RECEIVER_TOKEN-unset}"`)
		cmd.Env = nativeEnvironment(parent, s)
		raw, err := cmd.Output()
		want := strings.Join([]string{"/native/login", "/native/config", s.AssistantID, s.JobID, s.RunID, s.Mode, s.HostRoot, "unset", ""}, "\n")
		if err != nil || string(raw) != want {
			t.Fatalf("isolated child context: %q %v", raw, err)
		}
	}
}

func TestReceiverDriverCapabilitiesAreConservative(t *testing.T) {
	for _, r := range []Runtime{Codex(), Antigravity()} {
		d := r.ReceiverCapabilities()
		if d.Scope != "caller" {
			t.Fatal("receiver requires caller scope", d)
		}
		for _, op := range []agentlink.Operation{agentlink.TaskSubmit, agentlink.TaskRead, agentlink.TaskCancel, agentlink.TaskInput, agentlink.TaskEvents} {
			if !d.Supports(op) {
				t.Fatal(r.Kind, op)
			}
		}
		for _, op := range []agentlink.Operation{agentlink.TaskFiles, agentlink.Approvals, agentlink.ChatSend, agentlink.TaskResume, agentlink.TaskRetry, agentlink.TaskConfigure} {
			if d.Supports(op) {
				t.Fatal("unwired receiver capability", r.Kind, op)
			}
		}
	}
	if len((Runtime{Kind: "unknown"}).ReceiverCapabilities().Operations) != 0 {
		t.Fatal("unknown driver advertised")
	}
}

func TestNativeEnvironmentDoesNotMutateParent(t *testing.T) {
	parent := os.Environ()
	before := strings.Join(parent, "\n")
	_ = nativeEnvironment(parent, Session{JobID: "task", RunID: "run"})
	if strings.Join(parent, "\n") != before {
		t.Fatal("parent mutated")
	}
}

func TestCodexStartAndResumeReplaceOnlyExecutionContextConfiguration(t *testing.T) {
	worker, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root, workspace := t.TempDir(), t.TempDir()
	t.Setenv("POT_AGENT_JOB_ID", "parent-job")
	t.Setenv("POT_AGENT_RUN_ID", "parent-run")
	t.Setenv("POT_AGENT_ASSISTANT_ID", "parent-actor")
	thread := ""
	for i, run := range []string{"first-run", "next-run"} {
		s := Session{Binary: fakeBinary, Workspace: workspace, Mode: "read-only", Model: "execution-config-fixture", JobID: "task", RunID: run, HostRoot: root, ThreadID: thread, Worker: []string{worker}}
		result := runTurn(context.Background(), s, "configuration probe", turnEvents{})
		if result.Status != "completed" {
			t.Fatal(result)
		}
		if thread != "" && result.ThreadID != thread {
			t.Fatal("resume replaced thread")
		}
		thread = result.ThreadID
		raw, err := os.ReadFile(filepath.Join(workspace, "execution-config.json"))
		if err != nil {
			t.Fatal(err)
		}
		var got struct {
			Method, Job, Run, Assistant string
			Config                      map[string]struct {
				Command  string
				Args     []string
				Env      map[string]string
				Required bool
				Tools    []string `json:"enabled_tools"`
			}
		}
		if err = json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		method := "thread/start"
		if i > 0 {
			method = "thread/resume"
		}
		local := got.Config["mcp_servers.pot_receiver"]
		if got.Method != method || got.Job != "task" || got.Run != run || got.Assistant != "" || len(got.Config) != 1 || local.Command != worker || strings.Join(local.Args, "|") != "execution-tools|--root|"+root || !local.Required || strings.Join(local.Tools, ",") != "pot_execution_context" {
			t.Fatalf("incorrect child configuration: %s", raw)
		}
		if local.Env["POT_AGENT_JOB_ID"] != "task" || local.Env["POT_AGENT_RUN_ID"] != run || local.Env["POT_AGENT_ASSISTANT_ID"] != "" || len(local.Env) != 5 {
			t.Fatal("wrong execution identity", local.Env)
		}
	}
}

func TestCodexExecutionContextRejectsIncompleteBinding(t *testing.T) {
	for _, s := range []Session{{RunID: "run"}, {RunID: "run", JobID: "task", HostRoot: "relative"}} {
		if _, err := codexExecutionConfig(s); err == nil {
			t.Fatal("partial binding accepted")
		}
	}
}
