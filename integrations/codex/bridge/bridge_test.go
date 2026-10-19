package bridge

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jjyr/pot/pkg/processscope"
)

var fakeBinary string

func TestMain(m *testing.M) {
	if processscope.HandleWorker() {
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "mcp" {
		if err := Main(os.Args[1:], os.Stdin, os.Stdout); err != nil {
			os.Exit(1)
		}
		return
	}
	dir, err := os.MkdirTemp("", "pot-fake-codex-")
	if err != nil {
		panic(err)
	}
	fakeBinary = filepath.Join(dir, "codex")
	cmd := exec.Command("go", "build", "-o", fakeBinary, "./testdata/fake-codex")
	if output, e := cmd.CombinedOutput(); e != nil {
		panic(string(output))
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

type fixture struct {
	s             *Store
	server        *Server
	http          *httptest.Server
	remote, local ClientConfig
	cancel        context.CancelFunc
	t             *testing.T
}

func setup(t *testing.T) *fixture {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "bridge.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	exe, _ := os.Executable()
	server := NewServer(ctx, store, Config{Codex: fakeBinary, Workspaces: map[string]string{"repo": t.TempDir()}}, []string{exe})
	server.Heartbeat = 20 * time.Millisecond
	remote, local, err := store.Pair(Pair{Device: "A", Control: "control", Assistant: "main", Workspaces: []string{"repo"}})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewTLSServer(server)
	remote.Endpoint = httpServer.URL
	local.Endpoint = httpServer.URL
	f := &fixture{store, server, httpServer, remote, local, cancel, t}
	t.Cleanup(func() { cancel(); server.Wait(); httpServer.Close(); store.Close() })
	return f
}
func (f *fixture) call(client ClientConfig, method, path string, payload any, status int) []byte {
	f.t.Helper()
	var data io.Reader
	if payload != nil {
		data = strings.NewReader(encoded(payload))
	}
	req, _ := http.NewRequest(method, f.http.URL+path, data)
	req.Header.Set("Authorization", "Bearer "+client.Token)
	resp, err := f.http.Client().Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != status {
		f.t.Fatalf("%s %s status=%d want=%d body=%s", method, path, resp.StatusCode, status, body)
	}
	return body
}

func (f *fixture) tool(client ClientConfig, name string, args any, wantError string) []byte {
	f.t.Helper()
	req, _ := http.NewRequest("POST", f.http.URL+"/mcp", strings.NewReader(encoded(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": name, "arguments": args},
	})))
	req.Header.Set("Authorization", "Bearer "+client.Token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", "2025-06-18")
	resp, err := f.http.Client().Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var result struct {
		Error  json.RawMessage `json:"error"`
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if resp.StatusCode != 200 || json.Unmarshal(raw, &result) != nil || len(result.Error) > 0 || result.Result.IsError {
		if wantError == "" || wantError != "error" && !strings.Contains(string(raw), wantError) {
			f.t.Fatalf("%s: %s %s", name, resp.Status, raw)
		}
		return raw
	}
	if wantError != "" || len(result.Result.Content) != 1 {
		f.t.Fatalf("%s: expected error %q, got %s", name, wantError, raw)
	}
	return []byte(result.Result.Content[0].Text)
}
func (f *fixture) submit(id, prompt string) Task {
	f.t.Helper()
	raw := f.tool(f.remote, "codex_submit", Request{id, "repo", "read-only", prompt}, "")
	var task Task
	_ = json.Unmarshal(raw, &task)
	return task
}
func (f *fixture) task(id string) Task {
	var task Task
	_ = json.Unmarshal(f.tool(f.remote, "codex_status", map[string]string{"task_id": id}, ""), &task)
	return task
}
func (f *fixture) wait(id string, predicate func(Task) bool) Task {
	f.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		task := f.task(id)
		if predicate(task) {
			return task
		}
		time.Sleep(10 * time.Millisecond)
	}
	f.t.Fatal("task wait timed out", f.task(id))
	return Task{}
}
func TestAuthenticationIdempotencyAndIsolation(t *testing.T) {
	f := setup(t)
	bad := f.remote
	bad.Token = "bad"
	f.call(bad, "GET", "/v1/identity", nil, 401)
	a := f.submit("same", "literal $(touch /tmp/no-shell) `echo bad`")
	b := f.submit("same", a.Prompt)
	if a.ID != b.ID {
		t.Fatal("duplicate task")
	}
	f.tool(f.remote, "codex_submit", Request{"same", "repo", "read-only", "different"}, "request_id_conflict")
	for _, r := range []Request{{"path", "/etc", "read-only", "x"}, {"write", "repo", "workspace-write", "x"}, {"mode", "repo", "danger-full-access", "x"}} {
		f.tool(f.remote, "codex_submit", r, "error")
	}
	f.tool(f.local, "codex_submit", Request{"local", "repo", "read-only", "x"}, "error")
	r, _, _ := f.s.Pair(Pair{Control: "other", Assistant: "main", Workspaces: []string{"repo"}})
	f.tool(r, "codex_status", map[string]string{"task_id": a.ID}, "not_found")
	f.tool(r, "codex_cancel", map[string]string{"task_id": a.ID}, "not_found")
	f.tool(f.remote, "codex_submit", map[string]any{"request_id": "inject", "workspace": "repo", "mode": "read-only", "prompt": "x", "assistant": "other"}, "error")
	f.tool(f.remote, "codex_submit", Request{"huge", "repo", "read-only", strings.Repeat("x", maxBody+1)}, "error")
}
func TestAppServerSubprocessCompletionApprovalAndCancellation(t *testing.T) {
	f := setup(t)
	f.server.Start()
	a := f.submit("one", "literal $() `not-shell`")
	done := f.wait(a.ID, func(t Task) bool { return terminal(t.Status) })
	if done.Status != "completed" || strings.TrimSpace(done.Result) != "result: "+a.Prompt || done.ThreadID != "thread-1" || done.TurnID != "turn-1" {
		t.Fatal(done)
	}
	a = f.submit("approval", "approve")
	done = f.wait(a.ID, func(t Task) bool { return terminal(t.Status) })
	if done.Status != "failed" || !strings.Contains(done.Error, "approval_denied") {
		t.Fatal(done)
	}
	var count int
	_ = f.s.db.QueryRow("SELECT COUNT(*) FROM notices WHERE json_extract(data,'$.kind')='approval'").Scan(&count)
	if count != 1 {
		t.Fatal("missing visible approval", count)
	}
	a = f.submit("cancel", "hang")
	f.wait(a.ID, func(t Task) bool { return t.TurnID != "" })
	f.tool(f.remote, "codex_cancel", map[string]string{"task_id": a.ID}, "")
	if f.task(a.ID).Status != "cancelling" {
		t.Fatal("premature cancellation confirmation")
	}
	done = f.wait(a.ID, func(t Task) bool { return terminal(t.Status) })
	if done.Status != "cancelled" {
		t.Fatal(done)
	}
}
func TestRestartFencesQueuedAndRunningWithoutReplay(t *testing.T) {
	f := setup(t)
	a := f.submit("queued", "x")
	b := f.submit("running", "x")
	_ = f.s.update(f.remote.ID, b.ID, func(t *Task) bool { t.Status = "running"; return true }, "task", false)
	if err := f.s.Recover(); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{a.ID, b.ID} {
		task := f.task(id)
		reason := "bridge_restarted_outcome_unknown"
		if id == a.ID {
			reason = "bridge_restarted_before_execution"
		}
		if task.Status != "failed" || task.Error != reason {
			t.Fatal(task)
		}
	}
	if again := f.submit("queued", "x"); again.ID != a.ID || again.Status != "failed" {
		t.Fatal("replayed", again)
	}
}
func openSSE(t *testing.T, f *fixture, cursor string) (*http.Response, *bufio.Reader) {
	t.Helper()
	req, _ := http.NewRequest("GET", f.http.URL+"/v1/events?after=0", nil)
	req.Header.Set("Authorization", "Bearer "+f.remote.Token)
	if cursor != "" {
		req.Header.Set("Last-Event-ID", cursor)
	}
	resp, err := f.http.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp, bufio.NewReader(resp.Body)
}
func readFrame(t *testing.T, r *bufio.Reader) string {
	t.Helper()
	var b strings.Builder
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		b.WriteString(line)
		if line == "\n" {
			return b.String()
		}
	}
}
func TestSSERealHTTPReplayRegistrationHeartbeatAndExpiry(t *testing.T) {
	f := setup(t)
	resp, r := openSSE(t, f, "")
	defer resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(readFrame(t, r), "hello") {
		t.Fatal("missing hello")
	}
	if !strings.Contains(readFrame(t, r), "heartbeat") {
		t.Fatal("heartbeat")
	}
	a := f.submit("live", "stream")
	frame := readFrame(t, r)
	if !strings.Contains(frame, a.ID) || !strings.Contains(frame, "id: 1\n") {
		t.Fatal(frame)
	}
	f.submit("replay", "two")
	resp.Body.Close()
	waitStreams(t, f, 0)
	resp, r = openSSE(t, f, "1")
	defer resp.Body.Close()
	_ = readFrame(t, r)
	if frame = readFrame(t, r); !strings.Contains(frame, "id: 2\n") {
		t.Fatal(frame)
	}
	resp.Body.Close()
	waitStreams(t, f, 0)
	// Force the retention window without allocating per-token history.
	if _, err := f.s.db.Exec("DELETE FROM events WHERE cursor<2"); err != nil {
		t.Fatal(err)
	}
	resp, _ = openSSE(t, f, "0")
	if resp.StatusCode != 409 {
		t.Fatal(resp.StatusCode)
	}
	resp.Body.Close()
	resp, _ = openSSE(t, f, "99999")
	if resp.StatusCode != 409 {
		t.Fatal(resp.StatusCode)
	}
	resp.Body.Close()
	var snapshot map[string]any
	_ = json.Unmarshal(f.call(f.remote, "GET", "/v1/snapshot", nil, 200), &snapshot)
	if snapshot["cursor"] != float64(2) {
		t.Fatal(snapshot)
	}
}
func TestSlowConsumerJournalAndStreamBounds(t *testing.T) {
	f := setup(t)
	resp, _ := openSSE(t, f, "")
	defer resp.Body.Close()
	second, _ := openSSE(t, f, "")
	defer second.Body.Close()
	third, _ := openSSE(t, f, "")
	if third.StatusCode != 429 {
		t.Fatal(third.StatusCode)
	}
	third.Body.Close()
	for i := 0; i < retention+8; i++ {
		if err := f.s.mutate(func(tx *sql.Tx) error { return f.s.publish(tx, f.remote.ID, "progress", map[string]int{"n": i}, false) }); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	_ = f.s.db.QueryRow("SELECT COUNT(*) FROM events").Scan(&count)
	if count != retention {
		t.Fatal(count)
	}
}
func TestMCPStdioToolsCallsAndReceipt(t *testing.T) {
	f := setup(t)
	c := f.local
	cert := f.http.Certificate()
	c.CA = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}))
	path := filepath.Join(t.TempDir(), "local.json")
	if err := secureJSON(path, c); err != nil {
		t.Fatal(err)
	}
	exe, _ := os.Executable()
	cmd := exec.Command(exe, "mcp", "--config", path)
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		stdin.Close()
		if err := cmd.Wait(); err != nil {
			t.Error(err, stderr.String())
		}
	}()
	reader := bufio.NewReader(stdout)
	call := func(id int, method string, params any) map[string]any {
		t.Helper()
		_ = json.NewEncoder(stdin).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
		line, err := reader.ReadBytes('\n')
		if err != nil {
			t.Fatal(err)
		}
		var v map[string]any
		if json.Unmarshal(line, &v) != nil {
			t.Fatal(string(line))
		}
		if v["error"] != nil {
			t.Fatal(v)
		}
		return v["result"].(map[string]any)
	}
	result := call(1, "initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]string{"name": "test", "version": "1"}})
	if result["protocolVersion"] != "2025-06-18" {
		t.Fatal(result)
	}
	// A notification must be forwarded without introducing a stdout response.
	_ = json.NewEncoder(stdin).Encode(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	result = call(2, "tools/list", map[string]any{})
	if len(result["tools"].([]any)) != 3 {
		t.Fatal(result)
	}
	result = call(3, "tools/call", map[string]any{"name": "pot_request", "arguments": map[string]any{"request_id": "from-codex", "body": "please inspect", "correlation": "origin", "depth": 1}})
	if result["isError"] == true {
		t.Fatal(result)
	}
	var msg Message
	_ = json.Unmarshal([]byte(result["content"].([]any)[0].(map[string]any)["text"].(string)), &msg)
	if msg.Status != "accepted" {
		t.Fatal(msg)
	}
	var noticeID string
	_ = f.s.db.QueryRow("SELECT id FROM notices WHERE pair=?", f.remote.ID).Scan(&noticeID)
	f.call(f.remote, "POST", "/v1/receipts/"+noticeID, map[string]any{}, 200)
	result = call(4, "tools/call", map[string]any{"name": "pot_receipt", "arguments": map[string]any{"message_id": msg.ID}})
	if !strings.Contains(encoded(result), "received") {
		t.Fatal(result)
	}
	f.tool(f.remote, "codex_reply", map[string]string{"message_id": msg.ID, "result": "authorized answer"}, "")
	result = call(5, "tools/call", map[string]any{"name": "pot_receipt", "arguments": map[string]any{"message_id": msg.ID}})
	if !strings.Contains(encoded(result), "authorized answer") {
		t.Fatal(result)
	}
	f.tool(f.remote, "codex_reply", map[string]string{"message_id": msg.ID, "result": "different"}, "request_id_conflict")
}

func waitStreams(t *testing.T, f *fixture, n int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		f.server.mu.Lock()
		count := f.server.streams[f.remote.ID]
		f.server.mu.Unlock()
		if count == n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("stream cleanup timeout")
}

func TestEventProjectionEscapingAndSnapshotPagination(t *testing.T) {
	f := setup(t)
	for i := 0; i < 10; i++ {
		raw := f.tool(f.local, "pot_request", map[string]any{"request_id": fmt.Sprint("message-", i), "body": strings.Repeat("\x00", 8000)}, "")
		var msg Message
		if json.Unmarshal(raw, &msg) != nil {
			t.Fatal("message result")
		}
	}
	var snapshot struct {
		Notices []Notice `json:"notices"`
		More    bool     `json:"more"`
		Cursor  int64    `json:"cursor"`
	}
	_ = json.Unmarshal(f.call(f.remote, "GET", "/v1/snapshot", nil, 200), &snapshot)
	if len(snapshot.Notices) != 8 || !snapshot.More {
		t.Fatal("snapshot not bounded")
	}
	for _, n := range snapshot.Notices {
		if len(n.Data) > 20000 {
			t.Fatal("JSON escaping escaped bound", len(n.Data))
		}
		if !strings.Contains(string(n.Data), "truncated") {
			t.Fatal("missing preview marker")
		}
		f.call(f.remote, "POST", "/v1/receipts/"+n.ID, map[string]any{}, 200)
	}
	_ = json.Unmarshal(f.call(f.remote, "GET", "/v1/snapshot", nil, 200), &snapshot)
	if len(snapshot.Notices) != 2 || snapshot.More {
		t.Fatal("missing page", len(snapshot.Notices), snapshot.More)
	}
	projection := noticeData(Task{ID: "task", Status: "completed", Request: Request{Prompt: strings.Repeat("secret prompt", 1000)}, Result: strings.Repeat("\x00", maxResult)})
	if len(projection) > 20000 || strings.Contains(string(projection), "secret prompt") {
		t.Fatal("unbounded event or progress carries original prompt")
	}
}

func TestGracefulStopDrainsOriginalCodexProcess(t *testing.T) {
	f := setup(t)
	f.server.Start()
	task := f.submit("stop", "hang")
	f.wait(task.ID, func(t Task) bool { return t.TurnID != "" })
	f.server.mu.Lock()
	pid := f.server.active[task.ID].cmd.Process.Pid
	f.server.mu.Unlock()
	f.cancel()
	f.server.Wait()
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatal("supervisor still live", err)
	}
	if got := f.task(task.ID); got.Status != "failed" || !strings.Contains(got.Error, "unknown") {
		t.Fatal(got)
	}
}

func TestUnknownCleanupFencesAdmissionAndQueuedExecution(t *testing.T) {
	f := setup(t)
	task := f.submit("uncertain", "original")
	if err := f.s.update(f.remote.ID, task.ID, func(t *Task) bool {
		t.Status = "failed"
		t.Error = "codex_cleanup_unconfirmed_outcome_unknown"
		return true
	}, "terminal", true); err != nil {
		t.Fatal(err)
	}
	f.tool(f.remote, "codex_submit", Request{"new", "repo", "read-only", "new work"}, "cleanup_unconfirmed_requires_operator_reconciliation")
	// The original request identity remains inspectable and idempotent.
	if got := f.submit("uncertain", "original"); got.ID != task.ID || got.Error == "" {
		t.Fatal(got)
	}
}

func TestCLIConfigAndPairingFilesAreConfinedAndRepeatSafe(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "bridge.json")
	workspace := t.TempDir()
	c := Config{Listen: "0.0.0.0:8791", Database: "bridge.sqlite", Codex: fakeBinary, Workspaces: map[string]string{"repo": workspace}}
	if err := secureJSON(config, c); err != nil {
		t.Fatal(err)
	}
	if _, err := readConfig(config); err == nil {
		t.Fatal("wildcard bind accepted")
	}
	c.Listen = "100.64.1.2:8791"
	_ = os.WriteFile(config, []byte(encoded(c)), 0600)
	if _, err := readConfig(config); err == nil {
		t.Fatal("non-loopback cleartext accepted")
	}
	c.Listen = "127.0.0.1:8791"
	_ = os.WriteFile(config, []byte(encoded(c)), 0600)
	var out bytes.Buffer
	if err := Main([]string{"check", "--config", config}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	remote := filepath.Join(dir, "remote.json")
	local := filepath.Join(dir, "local.json")
	args := []string{"pair", "--config", config, "--endpoint", "https://bridge.example.test", "--control", "control", "--assistant", "main", "--workspaces", "repo", "--remote-file", remote, "--local-file", local}
	if err := Main(args, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(remote)
	if strings.Contains(out.String(), "\"token\"") {
		t.Fatal("token printed")
	}
	if err := Main(args, strings.NewReader(""), &out); err == nil {
		t.Fatal("pairing overwrote existing credentials")
	}
	after, _ := os.ReadFile(remote)
	if !bytes.Equal(before, after) {
		t.Fatal("credentials changed")
	}
	st, _ := os.Stat(local)
	if st.Mode().Perm() != 0600 {
		t.Fatal(st.Mode())
	}
	var r, l ClientConfig
	_ = json.Unmarshal(before, &r)
	data, _ := os.ReadFile(local)
	_ = json.Unmarshal(data, &l)
	if r.Token == l.Token || r.Route != "control/main" || l.Endpoint != "http://127.0.0.1:8791" {
		t.Fatal("pairing identities not separated")
	}
}
