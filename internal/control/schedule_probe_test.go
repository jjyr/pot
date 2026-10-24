package control

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/schedule"
	"github.com/jjyr/pot/internal/tasks"
	"github.com/jjyr/pot/internal/testutil"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/modelprotocol"
	"github.com/jjyr/pot/pkg/protocol"
)

func waitScheduleProbe(t *testing.T, s *Service, id string) protocol.ScheduleRun {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		page, err := s.Schedules.Page(context.Background(), "main", schedule.Query{Selected: id})
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range page.Runs {
			if r.TaskID != nil || r.FinishedAtMS != nil || r.State == "hook_blocked" {
				return r
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("probe timeout")
	return protocol.ScheduleRun{}
}
func TestScheduleProbeRealScriptToolToHistory(t *testing.T) {
	t.Parallel()
	foundSchema := false
	for _, definition := range sProductSchemasForProbeTest() {
		if definition.Name == "manage_schedule" {
			var schema map[string]any
			if err := json.Unmarshal(definition.InputSchema, &schema); err != nil {
				t.Fatal(err)
			}
			properties := schema["properties"].(map[string]any)["spec"].(map[string]any)["properties"].(map[string]any)
			if properties["before_run"] == nil {
				t.Fatal("tool schema omits probe")
			}
			foundSchema = true
		}
	}
	if !foundSchema {
		t.Fatal("manage_schedule tool missing")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"run", "skip", "stderr_ok", "utf8", "empty", "missing", "invalid", "enum", "nonzero", "overflow", "stderr", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			testScheduleProbeHistory(t, mode, python)
		})
	}
}
func TestScheduleProbeSecretIsolation(t *testing.T) {
	t.Setenv("POT_TEST_PROBE_SECRET", "never-inherit")
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	testScheduleProbeHistory(t, "skip", python)
}

func testScheduleProbeHistory(t *testing.T, mode, python string) {
	t.Helper()
	var err error
	s, _ := batchService(t, func(http.ResponseWriter, *http.Request) { t.Error("probe called a model") }, 1)
	// Keep accepted tasks at the preparation boundary: their real run receipts
	// exist, while model execution and task-result delivery belong to other tests.
	prepared := make(chan struct{})
	s.TaskRuns.Prepare = func(ctx context.Context, _ tasks.Run) (tasks.Prepared, error) {
		close(prepared)
		<-ctx.Done()
		return tasks.Prepared{}, ctx.Err()
	}
	if err := s.Permissions.Configure("local", MachinePolicy{ExecutionAccess: "allow"}); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "probe.py")
	body := `import json,sys,os,subprocess,time
mode=sys.argv[1]
context=json.load(sys.stdin)
assert set(context)=={'schedule_id','occurrence_id','trigger','scheduled_at','timezone'}
assert context['trigger']=='manual' and context['timezone']=='UTC' and context['scheduled_at'].endswith('Z')
assert 'GH_TOKEN' not in os.environ and 'POT_TEST_PROBE_SECRET' not in os.environ
with open('calls','a') as f: f.write(context['occurrence_id']+'\n')
if mode=='run': print('{"status":"run"}')
if mode=='skip': print('{"status":"skip","reason":"没有新内容"}')
if mode=='stderr_ok': print('{"status":"skip"}'); print('private diagnostic must be discarded',file=sys.stderr)
if mode=='utf8': sys.stdout.buffer.write(b'{"status":"skip","reason":"\xff"}')
if mode=='missing': print('{"reason":"omitted"}')
if mode=='invalid': print('{"status":"skip","goal":"do not accept"}')
if mode=='enum': print('{"status":"other"}')
if mode=='nonzero': print('{"status":"skip"}'); sys.exit(7)
if mode=='overflow': print('{"status":"skip"}'+(' '*20000))
if mode=='stderr': print('{"status":"skip"}'); print('diagnostic '*3000,file=sys.stderr)
if mode=='timeout':
 p=subprocess.Popen([sys.executable,'-c','import time;time.sleep(60)'],start_new_session=True)
 with open('pid','w') as f: f.write(str(p.pid))
 print('{"status":"skip"}',flush=True)
 time.sleep(60)
`
	if err = os.WriteFile(script, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{"action": "create", "spec": map[string]any{"title": "Probe fixture", "goal": "Fixture only", "kind": "interval", "every_minutes": 1, "timezone": "UTC", "enabled": false, "before_run": map[string]any{"command": []string{python, script, mode}, "cwd": dir, "timeout_seconds": 1}}})
	raw, err := s.productDispatch("main", "probe-tool", 1, false)["manage_schedule"](context.Background(), core.ExecutionContext{Owner: "main"}, modelprotocol.ToolCall{ID: "create", Name: "manage_schedule", Arguments: args}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var info protocol.ScheduleInfo
	if err = json.Unmarshal(raw, &info); err != nil {
		t.Fatal(err)
	}
	if info.Spec.BeforeRun == nil {
		t.Fatal(string(raw))
	}
	args, _ = json.Marshal(map[string]string{"action": "run", "schedule_id": info.ID})
	first, err := s.productCall(context.Background(), "main", "run", "manage_schedule", args)
	if err != nil {
		t.Fatal(err)
	}
	run := waitScheduleProbe(t, s, info.ID)
	second, err := s.productCall(context.Background(), "main", "run", "manage_schedule", args)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(first)
	b, _ := json.Marshal(second)
	if string(a) != string(b) {
		t.Fatal("retry changed receipt")
	}
	want := "error"
	if mode == "run" || mode == "empty" || mode == "missing" {
		want = "run"
	}
	if mode == "skip" || mode == "stderr_ok" {
		want = "skip"
	}
	if run.Hook == nil || run.Hook.Status != want || run.Hook.ExecutionMode != "host" {
		t.Fatalf("%s run=%+v hook=%+v", mode, run, run.Hook)
	}
	page, err := s.Tasks.Page(context.Background(), "main", protocol.TaskQuery{Scope: "all"})
	if err != nil {
		t.Fatal(err)
	}
	count := uint64(1)
	if mode == "skip" || mode == "stderr_ok" {
		count = 0
	}
	if page.Total != count {
		t.Fatal(page)
	}
	if count != 0 {
		if run.TaskID == nil {
			t.Fatal("probe result has no accepted task", run)
		}
		// Schedule acceptance precedes asynchronous run creation. Preparation
		// signals that the real run receipt exists, without completing the task.
		ctx, cancel := context.WithTimeout(context.Background(), testutil.Timeout(t, 10*time.Second))
		defer cancel()
		select {
		case <-prepared:
		case <-ctx.Done():
			t.Fatal("accepted probe task did not reach preparation", ctx.Err())
		}
	}
	calls, err := os.ReadFile(filepath.Join(dir, "calls"))
	if err != nil || strings.Count(string(calls), "\n") != 1 {
		t.Fatal(string(calls), err)
	}
	var sessions int
	if err = s.Tasks.DB().QueryRow("SELECT count(*) FROM task_runs").Scan(&sessions); err != nil || sessions != int(count) {
		t.Fatal("unexpected execution sessions after probe result", sessions, err)
	}
	var messages int
	if err = s.Tasks.DB().QueryRow("SELECT count(*) FROM agent_messages WHERE json_extract(envelope,'$.to.kind')='assistant'").Scan(&messages); err != nil || messages != 0 {
		t.Fatal("probe woke inbox", messages, err)
	}
	if len(run.Hook.Diagnostic) > 2000 || (mode == "skip" || mode == "stderr_ok") && run.Hook.Diagnostic != "" {
		t.Fatal(run.Hook)
	}
	if mode == "timeout" {
		data, err := os.ReadFile(filepath.Join(dir, "pid"))
		if err != nil {
			t.Fatal(err)
		}
		pid, _ := strconv.Atoi(string(data))
		if err = syscall.Kill(pid, 0); err != syscall.ESRCH {
			t.Fatal("detached child remains", pid, err)
		}
	}
	// UI/API and get_schedule use the same selected spec and history.
	details, err := s.productCall(context.Background(), "main", "get", "get_schedule", json.RawMessage(fmt.Sprintf(`{"schedule_id":%q}`, info.ID)))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(details)
	if !strings.Contains(string(raw), `"hook"`) || !strings.Contains(string(raw), `"before_run"`) {
		t.Fatal(string(raw))
	}
}

func TestScheduleProbePolicyAndReceiptRecovery(t *testing.T) {
	t.Parallel()
	s, _ := batchService(t, func(http.ResponseWriter, *http.Request) { t.Error("model called") }, 1)
	dir := t.TempDir()
	spec := protocol.ScheduleSpec{MachineID: "local", Title: "Fixture", Goal: "Fixture", Timezone: "UTC", Kind: "interval", EveryMinutes: 1, Misfire: "coalesce", Notify: "all", BeforeRun: &protocol.ScheduleBeforeRun{Command: []string{"/bin/sh", "-c", "printf x >> calls; printf '{\"status\":\"skip\"}'"}, CWD: dir}}
	r := schedule.ProbeRequest{Owner: "main", ScheduleID: "fixture", OccurrenceID: "occurrence", Trigger: "manual", ScheduledAt: "2026-10-16T00:00:00Z", Timezone: "UTC", Plan: schedule.Plan{Info: protocol.ScheduleInfo{ID: "fixture", Spec: spec}}}
	if err := s.Permissions.Configure("local", MachinePolicy{ExecutionAccess: "allow"}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		result, err := (scheduleProbes{s}).RunProbe(context.Background(), r)
		if err != nil || result.Status != "skip" {
			t.Fatal(result, err)
		}
	}
	data, _ := os.ReadFile(filepath.Join(dir, "calls"))
	if string(data) != "x" {
		t.Fatal("operation receipt reran script", string(data))
	}
	// Simulate the durable crossing before a crash. Recovery cannot infer cleanup
	// or launch a replacement, even though this fixture did not actually start it.
	r.OccurrenceID = "unknown"
	args, _ := json.Marshal(r)
	op := Operation{Owner: "schedule-probe-fixture", Turn: r.OccurrenceID, Generation: 1, CallID: "before_run", Target: core.ExecutionTarget{RuntimeID: "local"}, Tool: "schedule_before_run", Args: args}
	receipt, claimed, err := s.Operations.Begin(context.Background(), op)
	if err != nil || !claimed {
		t.Fatal(err)
	}
	_ = receipt
	if err = s.Operations.Close(); err != nil {
		t.Fatal(err)
	}
	s.Operations, err = OpenOperations(filepath.Join(s.options.Root, "operations.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = (scheduleProbes{s}).RunProbe(context.Background(), r); !core.IsCleanupUnconfirmed(err) {
		t.Fatal("uncertain receipt replayed", err)
	}
	// A denied policy is a known pre-start error and defaults to normal execution.
	r.ScheduleID = "denied"
	r.Plan.Info.ID = "denied"
	r.OccurrenceID = "denied"
	if err = s.Permissions.Configure("local", MachinePolicy{ExecutionAccess: "deny"}); err != nil {
		t.Fatal(err)
	}
	result, err := (scheduleProbes{s}).RunProbe(context.Background(), r)
	if err != nil || result.Status != "error" {
		t.Fatal(result, err)
	}
	data, _ = os.ReadFile(filepath.Join(dir, "calls"))
	if string(data) != "x" {
		t.Fatal("denied script ran")
	}
}

func TestScheduleProbeWorkingDirectoryResolution(t *testing.T) {
	t.Parallel()
	s, _ := batchService(t, func(http.ResponseWriter, *http.Request) { t.Error("model called") }, 1)
	if err := s.Permissions.Configure("local", MachinePolicy{ExecutionAccess: "allow"}); err != nil {
		t.Fatal(err)
	}
	workspace, err := s.ExecutionWorkspace("main")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(filepath.Join(workspace, "sub"), 0700); err != nil {
		t.Fatal(err)
	}
	for i, cwd := range []string{"", "sub", t.TempDir()} {
		spec := protocol.ScheduleSpec{MachineID: "local", BeforeRun: &protocol.ScheduleBeforeRun{Command: []string{"/bin/sh", "-c", `printf '{"status":"skip","reason":"%s"}' "$PWD"`}, CWD: cwd}}
		r := schedule.ProbeRequest{Owner: "main", ScheduleID: fmt.Sprint("cwd", i), OccurrenceID: "one", Plan: schedule.Plan{Info: protocol.ScheduleInfo{Spec: spec}}}
		result, err := (scheduleProbes{s}).RunProbe(context.Background(), r)
		want := cwd
		if cwd == "" {
			want = workspace
		} else if !filepath.IsAbs(cwd) {
			want = filepath.Join(workspace, cwd)
		}
		want, _ = filepath.EvalSymlinks(want)
		if err != nil || result.Status != "skip" || result.Reason != want {
			t.Fatal(result, err, want)
		}
	}
}

func sProductSchemasForProbeTest() []modelprotocol.ToolDefinition {
	var out []modelprotocol.ToolDefinition
	for _, raw := range core.ProductTools() {
		var definition modelprotocol.ToolDefinition
		if json.Unmarshal(raw, &definition) == nil {
			out = append(out, definition)
		}
	}
	return out
}
