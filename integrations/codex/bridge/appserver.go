package bridge

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jjyr/pot/pkg/processscope"
)

type rpcMessage struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
}
type appSession struct {
	readerDone                 chan struct{}
	cancel                     context.CancelFunc
	server                     *Server
	pair, id                   string
	cmd                        *exec.Cmd
	scope                      *processscope.Scope
	input                      io.WriteCloser
	mu                         sync.Mutex
	writing                    sync.Mutex
	seq                        int
	pending                    map[int]chan rpcMessage
	thread, turn               string
	done                       chan struct{}
	once                       sync.Once
	status, diagnostic, result string
	denied                     bool
	lastProgress               time.Time
	files                      []string
	workspace                  string
}

func (a *appSession) send(v any) error {
	a.writing.Lock()
	defer a.writing.Unlock()
	return json.NewEncoder(a.input).Encode(v)
}
func (a *appSession) rpc(method string, params any) (json.RawMessage, error) {
	a.mu.Lock()
	a.seq++
	id := a.seq
	ch := make(chan rpcMessage, 1)
	a.pending[id] = ch
	a.mu.Unlock()
	defer func() { a.mu.Lock(); delete(a.pending, id); a.mu.Unlock() }()
	if err := a.send(map[string]any{"id": id, "method": method, "params": params}); err != nil {
		return nil, err
	}
	select {
	case m := <-ch:
		if len(m.Error) > 0 {
			return nil, errors.New("codex_rpc_rejected_" + method)
		}
		return m.Result, nil
	case <-a.server.ctx.Done():
		return nil, a.server.ctx.Err()
	case <-a.done:
		return nil, errors.New("codex_exited")
	case <-time.After(30 * time.Second):
		return nil, errors.New("codex_rpc_timeout_" + method)
	}
}
func (a *appSession) finish(status, diagnostic string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.once.Do(func() { a.status = status; a.diagnostic = diagnostic; close(a.done) })
}
func (a *appSession) interrupt() {
	a.mu.Lock()
	thread, turn := a.thread, a.turn
	a.mu.Unlock()
	if thread != "" && turn != "" {
		_ = a.send(map[string]any{"id": "interrupt", "method": "turn/interrupt", "params": map[string]string{"threadId": thread, "turnId": turn}})
	}
}
func (a *appSession) read(output io.Reader) {
	scanner := bufio.NewScanner(output)
	scanner.Buffer(make([]byte, 4096), 256*1024)
	for scanner.Scan() {
		var m rpcMessage
		if json.Unmarshal(scanner.Bytes(), &m) != nil {
			a.finish("failed", "codex_invalid_jsonl")
			return
		}
		if m.Method == "" {
			var id int
			if json.Unmarshal(m.ID, &id) == nil {
				a.mu.Lock()
				ch := a.pending[id]
				a.mu.Unlock()
				if ch != nil {
					select {
					case ch <- m:
					default:
						a.finish("failed", "codex_duplicate_response")
						return
					}
				}
			}
			continue
		}
		if len(m.ID) > 0 {
			a.approval(m)
			continue
		}
		a.notification(m)
	}
	a.finish("failed", "codex_process_ended_outcome_unknown")
}
func (a *appSession) approval(m rpcMessage) {
	a.mu.Lock()
	alreadyDenied := a.denied
	a.denied = true
	a.mu.Unlock()
	if alreadyDenied {
		a.finish("failed", "repeated_interactive_requests_denied")
		return
	}
	_ = a.server.Store.update(a.pair, a.id, func(t *Task) bool {
		if terminal(t.Status) {
			return false
		}
		if t.Status != "cancelling" {
			t.Status = "waiting_approval"
		}
		t.Error = "approval_denied_by_bridge_review_locally"
		return true
	}, "approval", true)
	// This unattended bridge cannot elevate authority or approve commands. Reject
	// known approvals and interrupt on other server-initiated requests.
	switch m.Method {
	case "item/commandExecution/requestApproval", "item/fileChange/requestApproval":
		_ = a.send(map[string]any{"id": m.ID, "result": map[string]string{"decision": "decline"}})
	case "mcpServer/elicitation/request":
		_ = a.send(map[string]any{"id": m.ID, "result": map[string]string{"action": "cancel"}})
	default:
		_ = a.send(map[string]any{"id": m.ID, "error": map[string]any{"code": -32601, "message": "Unattended bridge cannot approve or supply interactive input"}})
		a.interrupt()
	}
}
func (a *appSession) notification(m rpcMessage) {
	var p struct {
		ThreadID string `json:"threadId"`
		Turn     struct {
			ID     string          `json:"id"`
			Status string          `json:"status"`
			Error  json.RawMessage `json:"error"`
		} `json:"turn"`
		Item struct {
			Type    string `json:"type"`
			Text    string `json:"text"`
			Changes []struct {
				Path string `json:"path"`
			} `json:"changes"`
		} `json:"item"`
	}
	if json.Unmarshal(m.Params, &p) != nil {
		return
	}
	a.mu.Lock()
	thread := a.thread
	a.mu.Unlock()
	if thread != "" && p.ThreadID != "" && thread != p.ThreadID {
		return
	}
	switch m.Method {
	case "turn/started":
		a.mu.Lock()
		a.turn = p.Turn.ID
		a.mu.Unlock()
		a.started()
	case "turn/completed":
		status := p.Turn.Status
		diagnostic := ""
		switch status {
		case "completed":
		case "interrupted":
			status = "cancelled"
		case "failed":
			diagnostic = "codex_turn_failed"
		default:
			status = "failed"
			diagnostic = "codex_unknown_turn_status"
		}
		a.mu.Lock()
		denied := a.denied
		a.mu.Unlock()
		if denied {
			status = "failed"
			diagnostic = "approval_denied_by_bridge_review_locally"
		}
		a.finish(status, diagnostic)
	case "item/completed":
		a.mu.Lock()
		if p.Item.Type == "agentMessage" {
			if len(a.result)+len(p.Item.Text) <= maxResult {
				a.result += p.Item.Text + "\n"
			} else {
				a.result = trimBytes(a.result+p.Item.Text, maxResult)
			}
		}
		if p.Item.Type == "fileChange" {
			for _, c := range p.Item.Changes {
				path := c.Path
				if !filepath.IsAbs(path) {
					path = filepath.Join(a.workspace, path)
				}
				rel, err := filepath.Rel(a.workspace, path)
				if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && len(a.files) < 16 && len(rel) <= 256 {
					a.files = append(a.files, filepath.ToSlash(rel))
				}
			}
		}
		a.mu.Unlock()
	default:
		// Never persist token text or emit one assistant notification per token.
		a.mu.Lock()
		emit := time.Since(a.lastProgress) >= time.Second
		if emit {
			a.lastProgress = time.Now()
		}
		a.mu.Unlock()
		if emit && (strings.HasSuffix(m.Method, "/delta") || m.Method == "item/started") {
			_ = a.server.Store.update(a.pair, a.id, func(t *Task) bool { return !terminal(t.Status) }, "progress", false)
		}
	}
}
func trimBytes(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	return strings.ToValidUTF8(text[:limit], "")
}

func (a *appSession) started() {
	a.mu.Lock()
	thread, turn := a.thread, a.turn
	a.mu.Unlock()
	cancelled := false
	_ = a.server.Store.update(a.pair, a.id, func(t *Task) bool {
		cancelled = t.Status == "cancelling"
		if terminal(t.Status) {
			return false
		}
		if !cancelled {
			t.Status = "running"
		}
		t.ThreadID = thread
		t.TurnID = turn
		return true
	}, "task", false)
	if cancelled {
		a.interrupt()
	}
}
func (s *Server) execute(pair, id string) {
	var task Task
	err := s.Store.update(pair, id, func(t *Task) bool {
		if t.Status != "queued" {
			return false
		}
		task = *t
		t.Status = "running"
		return true
	}, "task", false)
	if err != nil || task.ID == "" {
		return
	}
	path, err := workspacePath(s.Config.Workspaces[task.Workspace])
	if err != nil {
		s.failTask(pair, id, "workspace_unavailable")
		return
	}
	a := &appSession{server: s, pair: pair, id: id, done: make(chan struct{}), pending: map[int]chan rpcMessage{}, workspace: path, readerDone: make(chan struct{})}
	s.mu.Lock()
	s.active[id] = a
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.active, id); s.mu.Unlock() }()
	if err = a.launch(path); err != nil {
		reason := "codex_unavailable_check_install_login_and_supervisor"
		if errors.Is(err, errUncertain) {
			reason = "codex_cleanup_unconfirmed_outcome_unknown"
		}
		s.failTask(pair, id, reason)
		return
	}
	// The final state is published only after the turn ended AND supervised
	// descendants were drained. An interrupt reply alone is never completion.
	defer func() {
		defer a.cancel()
		_ = a.scope.Stop(a.cmd.Process.Pid)
		_ = a.cmd.Wait()
		a.input.Close()
		<-a.readerDone
		cleanup := a.scope.Finish()
		a.mu.Lock()
		status, diagnostic, result := a.status, a.diagnostic, a.result
		files := append([]string{}, a.files...)
		a.mu.Unlock()
		if cleanup != nil {
			status = "failed"
			diagnostic = "codex_cleanup_unconfirmed_outcome_unknown"
		}
		if status == "" {
			status = "failed"
			diagnostic = "codex_outcome_unknown"
		}
		_ = s.Store.update(pair, id, func(t *Task) bool {
			t.Status = status
			t.Error = diagnostic
			t.Result = result
			t.Files = files
			return true
		}, "terminal", true)
	}()
	if _, err = a.rpc("initialize", map[string]any{"clientInfo": map[string]string{"name": "pot_codex_bridge", "version": "0.1.0"}, "capabilities": map[string]bool{"experimentalApi": false}}); err != nil {
		a.finish("failed", "codex_initialize_failed_check_install_and_login")
		return
	}
	if err = a.send(map[string]string{"method": "initialized"}); err != nil {
		a.finish("failed", "codex_initialized_failed")
		return
	}
	raw, err := a.rpc("thread/start", map[string]any{"cwd": path, "approvalPolicy": "on-request", "approvalsReviewer": "user", "sandbox": task.Mode, "ephemeral": true})
	if err != nil {
		a.finish("failed", "codex_thread_start_failed_check_login")
		return
	}
	var thread struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if json.Unmarshal(raw, &thread) != nil || thread.Thread.ID == "" {
		a.finish("failed", "codex_invalid_thread")
		return
	}
	a.mu.Lock()
	a.thread = thread.Thread.ID
	a.mu.Unlock()
	raw, err = a.rpc("turn/start", map[string]any{"threadId": thread.Thread.ID, "input": []any{map[string]any{"type": "text", "text": task.Prompt, "text_elements": []any{}}}})
	if err != nil {
		a.finish("failed", "codex_turn_start_failed_outcome_unknown")
		return
	}
	var turn struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if json.Unmarshal(raw, &turn) != nil || turn.Turn.ID == "" {
		a.finish("failed", "codex_invalid_turn_outcome_unknown")
		return
	}
	a.mu.Lock()
	a.turn = turn.Turn.ID
	a.mu.Unlock()
	a.started()
	select {
	case <-a.done:
	case <-s.ctx.Done():
		a.finish("failed", "bridge_stopped_outcome_unknown")
	case <-time.After(30 * time.Minute):
		a.finish("failed", "codex_deadline_outcome_unknown")
	}
}
func (s *Server) failTask(pair, id, reason string) {
	_ = s.Store.update(pair, id, func(t *Task) bool { t.Status = "failed"; t.Error = reason; return true }, "terminal", true)
}
func (a *appSession) launch(path string) error {
	binary := a.server.Config.Codex
	if binary == "" {
		binary = "codex"
	}
	binary, err := exec.LookPath(binary)
	if err != nil {
		return err
	}
	binary, err = filepath.Abs(binary)
	if err != nil {
		return err
	}
	taskCtx, taskCancel := context.WithTimeout(a.server.ctx, 30*time.Minute)
	cmd := exec.CommandContext(taskCtx, binary, "app-server", "--listen", "stdio://")
	cmd.Dir = path
	cmd.Env = os.Environ()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	input, err := cmd.StdinPipe()
	if err != nil {
		taskCancel()
		return err
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		taskCancel()
		input.Close()
		return err
	}
	cmd.Stderr = io.Discard
	scope, err := processscope.Prepare(cmd, a.server.Worker, processscope.Markers{})
	if err != nil {
		taskCancel()
		input.Close()
		return err
	}
	cmd.Cancel = func() error { return scope.Stop(cmd.Process.Pid) }
	cmd.WaitDelay = 3 * time.Second
	if err = cmd.Start(); err != nil {
		taskCancel()
		scope.Abort()
		input.Close()
		return err
	}
	if err = scope.Started(); err != nil {
		taskCancel()
		_ = scope.Stop(cmd.Process.Pid)
		_ = cmd.Wait()
		if scope.Finish() != nil {
			return errUncertain
		}
		return err
	}
	a.cancel = taskCancel
	a.cmd = cmd
	a.scope = scope
	a.input = input
	go func() { defer close(a.readerDone); a.read(output) }()
	return nil
}
